package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai"
	"github.com/quailyquaily/uniai/chat"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestEmbeddedConfigParses(t *testing.T) {
	cfg, err := loadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"openai/gpt-6-sol": "openai_resp", "anthropic/claude-sonnet-5-5": "anthropic", "moonshot/kimi-k3": "openai",
		"deepseek/deepseek-flash": "deepseek", "deepseek/deepseek-v4-pro": "deepseek", "gemini/gemini-4-hydrogen": "gemini",
	}
	for _, m := range cfg.Models {
		if route, ok := want[m.Name]; ok {
			if m.Provider != route {
				t.Errorf("%s: route %s, want %s", m.Name, m.Provider, route)
			}
			delete(want, m.Name)
		}
	}
	for name := range want {
		t.Errorf("default config is missing %s", name)
	}
}

// TestLocalConfigsParse validates gitignored local configs when present.
func TestLocalConfigsParse(t *testing.T) {
	paths, _ := filepath.Glob("*.local.yaml")
	if _, err := os.Stat("config.quick.yaml"); err == nil {
		paths = append(paths, "config.quick.yaml")
	}
	if len(paths) == 0 {
		t.Skip("no local configs")
	}
	for _, p := range paths {
		if _, err := loadConfig(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

const groupedYAML = `
providers:
  - name: openai
    provider: openai_resp
    reasoning_effort: low
    models:
      - gpt-6-luna
      - model: gpt-6-sol
        reasoning_effort: high
        cases: [text]
  - name: anthropic
    models: [claude-haiku-5-5]
  - name: open-router
    json_mode: prompt
    models: [anthropic/claude-haiku-5.5, openai/gpt-6-luna]
  - name: moonshot
    api_base: https://api.moonshot.ai/v1
    models: [kimi-k3]
  - name: abc
    provider: anthropic
    api_key_env: ABC_TOKEN
    models:
      - claude-haiku-5-5
      - model: claude-haiku-5-5
        name: haiku-no-cache
        cache_breakpoint: off
`

func TestParseGroupedConfig(t *testing.T) {
	cfg, err := parseConfig([]byte(groupedYAML))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]modelConfig{}
	var order []string
	for _, m := range cfg.Models {
		byName[m.Name] = m
		order = append(order, m.Name)
	}
	if got := strings.Join(order, ","); got != "openai/gpt-6-luna,openai/gpt-6-sol,anthropic/claude-haiku-5-5,open-router/anthropic/claude-haiku-5.5,open-router/openai/gpt-6-luna,moonshot/kimi-k3,abc/claude-haiku-5-5,abc/haiku-no-cache" {
		t.Fatalf("models not expanded in order: %s", got)
	}
	for _, tt := range []struct {
		name, route, effort, jsonMode, cache, keyEnvs, baseEnv, base string
		baseRequired                                                 bool
		cases                                                        string
	}{
		{"openai/gpt-6-luna", "openai_resp", "low", "", "", "OPENAI_API_KEY", "OPENAI_API_BASE", "", false, ""},
		{"openai/gpt-6-sol", "openai_resp", "high", "", "", "OPENAI_API_KEY", "OPENAI_API_BASE", "", false, "text"},
		{"anthropic/claude-haiku-5-5", "anthropic", "", "", "", "CLAUDE_API_KEY,ANTHROPIC_API_KEY", "ANTHROPIC_API_BASE", "", false, ""},
		{"open-router/openai/gpt-6-luna", "openai", "", "prompt", "", "OPEN_ROUTER_API_KEY", "OPEN_ROUTER_API_BASE", "", true, ""},
		{"moonshot/kimi-k3", "openai", "", "", "", "MOONSHOT_API_KEY", "MOONSHOT_API_BASE", "https://api.moonshot.ai/v1", true, ""},
		{"abc/claude-haiku-5-5", "anthropic", "", "", "", "ABC_TOKEN", "ABC_API_BASE", "", true, ""},
		{"abc/haiku-no-cache", "anthropic", "", "", "off", "ABC_TOKEN", "ABC_API_BASE", "", true, ""},
	} {
		m, ok := byName[tt.name]
		if !ok {
			t.Errorf("missing %s", tt.name)
			continue
		}
		if m.Provider != tt.route || m.ReasoningEffort != tt.effort || m.JSONMode != tt.jsonMode || m.CacheBreakpoint != tt.cache ||
			strings.Join(m.KeyEnvs, ",") != tt.keyEnvs || m.BaseEnv != tt.baseEnv || m.APIBase != tt.base ||
			m.BaseRequired != tt.baseRequired || strings.Join(m.Cases, ",") != tt.cases {
			t.Errorf("%s: %#v", tt.name, m)
		}
	}
	if byName["abc/haiku-no-cache"].Model != "claude-haiku-5-5" {
		t.Error("named entry lost its model ID")
	}
}

func TestParseConfigValidation(t *testing.T) {
	for _, tt := range []struct{ name, yaml, err string }{
		{"no providers", "providers: []", "no providers"},
		{"no name", "providers: [{models: [m]}]", "name is required"},
		{"no models", "providers: [{name: a}]", "has no models"},
		{"missing model", "providers: [{name: a, models: [{name: x}]}]", "model is required"},
		{"bad route", "providers: [{name: a, provider: nope, models: [m]}]", "unsupported provider route"},
		{"duplicate provider", "providers: [{name: a, models: [m]}, {name: a, models: [n]}]", "duplicate provider"},
		{"duplicate model", "providers: [{name: a, models: [m, m]}]", "give one a name"},
		{"bad json mode", "providers: [{name: a, json_mode: strict, models: [m]}]", "json_mode"},
		{"bad case", "providers: [{name: a, models: [{model: m, cases: [speed]}]}]", "unknown case"},
	} {
		if _, err := parseConfig([]byte(tt.yaml)); err == nil || !strings.Contains(err.Error(), tt.err) {
			t.Errorf("%s: got %v, want %q", tt.name, err, tt.err)
		}
	}
	cfg, err := parseConfig([]byte("providers: [{name: openai, models: [gpt-6-sol]}]"))
	if err != nil {
		t.Fatal(err)
	}
	if m := cfg.Models[0]; m.Provider != "openai" || m.Name != "openai/gpt-6-sol" || cfg.TimeoutSeconds != 180 || cfg.CacheAttempts != 3 {
		t.Fatalf("defaults not applied: %#v %#v", cfg, m)
	}
}

func TestResolveEnv(t *testing.T) {
	cfg, err := parseConfig([]byte(groupedYAML))
	if err != nil {
		t.Fatal(err)
	}
	models := map[string]modelConfig{}
	for _, m := range cfg.Models {
		models[m.Name] = m
	}
	env := envMap(map[string]string{
		"OPEN_ROUTER_API_KEY": "or", "OPEN_ROUTER_API_BASE": "https://openrouter.ai/api/v1",
		"ANTHROPIC_API_KEY": "ant", "OPENAI_API_KEY": "oa", "OPENAI_API_BASE": "https://gateway.example/v1",
		"MOONSHOT_API_KEY": "ms", "ABC_TOKEN": "abc",
	})
	for _, tt := range []struct {
		name, keyEnv, base, skip string
	}{
		{"open-router/openai/gpt-6-luna", "OPEN_ROUTER_API_KEY", "https://openrouter.ai/api/v1", ""},
		{"moonshot/kimi-k3", "MOONSHOT_API_KEY", "https://api.moonshot.ai/v1", ""},
		{"anthropic/claude-haiku-5-5", "ANTHROPIC_API_KEY", "", ""},
		{"openai/gpt-6-luna", "OPENAI_API_KEY", "https://gateway.example/v1", ""},
		{"abc/claude-haiku-5-5", "ABC_TOKEN", "", "ABC_API_BASE"},
	} {
		r, skip := resolveEnv(models[tt.name], env)
		if r.keyEnv != tt.keyEnv || r.apiBase != tt.base || (tt.skip == "") != (skip == "") || !strings.Contains(skip, tt.skip) {
			t.Errorf("%s: got %#v skip=%q", tt.name, r, skip)
		}
	}

	// CLAUDE_API_KEY takes precedence over ANTHROPIC_API_KEY; missing keys skip.
	anth := models["anthropic/claude-haiku-5-5"]
	if r, _ := resolveEnv(anth, envMap(map[string]string{"CLAUDE_API_KEY": "c", "ANTHROPIC_API_KEY": "a"})); r.keyEnv != "CLAUDE_API_KEY" {
		t.Errorf("expected CLAUDE_API_KEY, got %s", r.keyEnv)
	}
	if _, skip := resolveEnv(anth, envMap(nil)); !strings.Contains(skip, "CLAUDE_API_KEY / ANTHROPIC_API_KEY") {
		t.Errorf("unexpected skip %q", skip)
	}
}

func TestBuildClientConfig(t *testing.T) {
	env := envMap(map[string]string{"K": " secret "})
	r := resolved{keyEnv: "K", apiBase: "https://b"}
	if c := buildClientConfig(modelConfig{Provider: "gemini", Model: "g"}, r, env); c.GeminiAPIKey != "secret" || c.GeminiAPIBase != "https://b" || c.GeminiModel != "g" {
		t.Errorf("gemini: %#v", c)
	}
	if c := buildClientConfig(modelConfig{Provider: "anthropic", Model: "a"}, r, env); c.AnthropicAPIKey != "secret" || c.AnthropicModel != "a" {
		t.Errorf("anthropic: %#v", c)
	}
	if c := buildClientConfig(modelConfig{Provider: "deepseek", Model: "d"}, r, env); c.OpenAIAPIKey != "secret" || c.OpenAIModel != "d" || c.Provider != "deepseek" {
		t.Errorf("deepseek: %#v", c)
	}
}

func TestReplyCheckers(t *testing.T) {
	if err := checkJSONReply("```json\n{\"city\":\"Tokyo\",\"country\":\"Japan\",\"population_millions\":14}\n```"); err != nil {
		t.Errorf("fenced JSON should pass: %v", err)
	}
	for _, bad := range []string{"no json", `{"city":"Osaka","country":"Japan","population_millions":2}`, `{"city":"Tokyo","country":"Japan","population_millions":"14"}`} {
		if checkJSONReply(bad) == nil {
			t.Errorf("expected failure for %s", bad)
		}
	}
	call := func(name, args string) uniai.ToolCall {
		return uniai.ToolCall{Function: chat.ToolCallFunction{Name: name, Arguments: args}}
	}
	if err := checkWeatherCall([]uniai.ToolCall{call("get_weather", `{"city":"Tokyo, Japan"}`)}); err != nil {
		t.Errorf("valid call failed: %v", err)
	}
	for _, calls := range [][]uniai.ToolCall{nil, {call("other", `{}`)}, {call("get_weather", `{"city":"Paris"}`)}, {call("get_weather", `not json`)}} {
		if checkWeatherCall(calls) == nil {
			t.Errorf("expected failure for %#v", calls)
		}
	}
}

func TestCacheOptions(t *testing.T) {
	for _, tt := range []struct {
		name          string
		m             modelConfig
		base          string
		wantCtrl      bool
		wantTTL       string
		wantPromptKey bool
	}{
		{"anthropic", modelConfig{Provider: "anthropic", Model: "claude-sonnet-5-5"}, "", true, "5m", false},
		{"gpt-6 official", modelConfig{Provider: "openai_resp", Model: "gpt-6-sol"}, "", true, "", true},
		{"gpt-6 via gateway", modelConfig{Provider: "openai", Model: "gpt-6-sol"}, "https://openrouter.ai/api/v1", false, "", false},
		{"kimi", modelConfig{Provider: "openai", Model: "kimi-k3"}, "https://api.moonshot.ai/v1", false, "", false},
		{"deepseek", modelConfig{Provider: "deepseek", Model: "deepseek-flash"}, "", false, "", false},
		{"gemini", modelConfig{Provider: "gemini", Model: "gemini-4-argon"}, "", false, "", false},
		{"forced off", modelConfig{Provider: "anthropic", Model: "claude-haiku-5-5", modelSettings: modelSettings{CacheBreakpoint: "off"}}, "", false, "", false},
		{"forced on gateway", modelConfig{Provider: "openai", Model: "x", modelSettings: modelSettings{CacheBreakpoint: "on"}}, "https://gw/v1", true, "", false},
	} {
		r := &runner{model: tt.m, apiBase: tt.base, nonce: "n"}
		ctrl, extra := r.cacheOptions()
		if (ctrl != nil) != tt.wantCtrl || (ctrl != nil && ctrl.TTL != tt.wantTTL) || (extra != nil) != tt.wantPromptKey {
			t.Errorf("%s: ctrl=%#v extra=%v", tt.name, ctrl, extra != nil)
		}
	}
}

func TestJSONOption(t *testing.T) {
	for _, tt := range []struct {
		provider, mode, wantMode, wantKey string
		wantErr                           bool
	}{
		{"openai_resp", "", "native", "response_format", false},
		{"deepseek", "auto", "native", "response_format", false},
		{"gemini", "", "native", "response_mime_type", false},
		{"anthropic", "", "prompt", "", false},
		{"anthropic", "native", "", "", true},
		{"openai", "prompt", "prompt", "", false},
	} {
		r := &runner{model: modelConfig{Provider: tt.provider, modelSettings: modelSettings{JSONMode: tt.mode}}}
		mode, opt, err := r.jsonOption()
		if (err != nil) != tt.wantErr {
			t.Errorf("%s/%s: err=%v", tt.provider, tt.mode, err)
			continue
		}
		if err != nil {
			continue
		}
		req := &chat.Request{}
		if opt != nil {
			opt(req)
		}
		if mode != tt.wantMode || (tt.wantKey == "") != (len(req.Options.OpenAI) == 0) || (tt.wantKey != "" && !req.Options.OpenAI.HasKey(tt.wantKey)) {
			t.Errorf("%s/%s: mode=%s opts=%v", tt.provider, tt.mode, mode, req.Options.OpenAI)
		}
	}
}

// fakeChat records requests and replays scripted results.
type fakeChat struct {
	requests []*chat.Request
	results  []*uniai.ChatResult
}

func (f *fakeChat) chat(_ context.Context, opts ...uniai.ChatOption) (*uniai.ChatResult, error) {
	req := &chat.Request{}
	for _, o := range opts {
		o(req)
	}
	f.requests = append(f.requests, req)
	res := f.results[0]
	if len(f.results) > 1 {
		f.results = f.results[1:]
	}
	return res, nil
}

func TestRunnerCases(t *testing.T) {
	newRunner := func(f *fakeChat, m modelConfig) *runner {
		return &runner{chat: f.chat, model: m, maxTokens: 2048, timeout: 1e9, cacheAttempts: 3, nonce: "n1"}
	}
	m := modelConfig{Provider: "anthropic", Model: "claude-sonnet-5-5", modelSettings: modelSettings{ReasoningEffort: "low"}}

	f := &fakeChat{results: []*uniai.ChatResult{{Text: "modelcheck-ok"}}}
	if res := newRunner(f, m).run(context.Background(), caseText); res.Status != statusPass {
		t.Fatalf("text: %#v", res)
	}
	req := f.requests[0]
	if req.Model != "claude-sonnet-5-5" || req.Provider != "anthropic" || *req.Options.MaxTokens != 2048 || *req.Options.ReasoningEffort != "low" {
		t.Fatalf("base options not applied: %#v", req)
	}

	// Cache: cold, miss, then hit on the second repeat with identical prompts.
	miss := &uniai.ChatResult{Text: "cache-ok", Usage: uniai.Usage{InputTokens: 9000}}
	hit := &uniai.ChatResult{Text: "cache-ok", Usage: uniai.Usage{InputTokens: 9000, Cache: uniai.UsageCache{CachedInputTokens: 8800}}}
	miss.Usage.Cost = &uniai.UsageCost{Total: 0.01}
	hit.Usage.Cost = &uniai.UsageCost{Total: 0.002}
	f = &fakeChat{results: []*uniai.ChatResult{miss, miss, hit}}
	res := newRunner(f, m).run(context.Background(), caseCache)
	if res.Status != statusPass || res.Requests != 3 || !strings.Contains(res.Detail, "repeat 2") {
		t.Fatalf("cache: %#v", res)
	}
	if !res.Priced || res.CostUSD < 0.0219 || res.CostUSD > 0.0221 {
		t.Fatalf("cache cost should include every request: %v", res.CostUSD)
	}
	first, last := f.requests[0].Messages[0], f.requests[2].Messages[0]
	if len(first.Parts) != 2 || first.Parts[0].Text != last.Parts[0].Text || first.Parts[0].CacheControl == nil || first.Parts[0].CacheControl.TTL != "5m" {
		t.Fatalf("cache prefix not stable or not marked: %#v", first.Parts[0].CacheControl)
	}
	if n := len(strings.Fields(first.Parts[0].Text)); n < 5000 {
		t.Fatalf("cache prefix too small: %d words", n)
	}

	if res.CacheHitRate == nil || *res.CacheHitRate < 0.977 || *res.CacheHitRate > 0.978 || !strings.HasPrefix(res.Detail, "hit rate 97.8% on repeat 2") {
		t.Fatalf("hit rate: %v %q", res.CacheHitRate, res.Detail)
	}
	hitCell := caseResult{Case: caseCache, Status: statusPass, CacheHitRate: res.CacheHitRate}
	var table bytes.Buffer
	printTable(&table, painter{}, []modelResult{{Name: "m", Cases: []caseResult{hitCell}}}, []string{caseCache})
	if !strings.Contains(table.String(), "PASS 97.8% 0.0s") {
		t.Fatalf("table should show hit rate:\n%s", table.String())
	}

	f = &fakeChat{results: []*uniai.ChatResult{miss}}
	res = newRunner(f, m).run(context.Background(), caseCache)
	if res.Status != statusFail || res.Requests != 4 || res.CacheHitRate == nil || *res.CacheHitRate != 0 || !strings.HasPrefix(res.Detail, "hit rate 0% after 3 repeats") {
		t.Fatalf("cache miss should fail after 3 repeats: %#v", res)
	}
	table.Reset()
	printTable(&table, painter{}, []modelResult{{Name: "m", Cases: []caseResult{res}}}, []string{caseCache})
	if !strings.Contains(table.String(), "FAIL 0.0%") {
		t.Fatalf("table should show 0%% on a miss:\n%s", table.String())
	}

	// Tool: request declares get_weather and checks the returned call.
	f = &fakeChat{results: []*uniai.ChatResult{{ToolCalls: []uniai.ToolCall{{Function: chat.ToolCallFunction{Name: "get_weather", Arguments: `{"city":"Tokyo"}`}}}}}}
	if res := newRunner(f, m).run(context.Background(), caseTool); res.Status != statusPass {
		t.Fatalf("tool: %#v", res)
	}
	if tools := f.requests[0].Tools; len(tools) != 1 || tools[0].Function.Name != "get_weather" {
		t.Fatalf("tool not declared: %#v", tools)
	}

	// JSON through the native Gemini switch.
	f = &fakeChat{results: []*uniai.ChatResult{{Text: `{"city":"Tokyo","country":"Japan","population_millions":14.1}`}}}
	if res := newRunner(f, modelConfig{Provider: "gemini", Model: "gemini-4-argon"}).run(context.Background(), caseJSON); res.Status != statusPass || res.Detail != "json_mode=native" {
		t.Fatalf("json: %#v", res)
	}
}

func TestPricingCase(t *testing.T) {
	catalog := uniai.DefaultPricingCatalog()
	newRunner := func(f *fakeChat, model string) *runner {
		return &runner{chat: f.chat, model: modelConfig{Provider: "anthropic", Model: model}, timeout: 1e9, catalog: catalog}
	}
	priced := &uniai.ChatResult{Text: "modelcheck-ok", Model: "claude-haiku-5-5", Usage: uniai.Usage{Cost: &uniai.UsageCost{Total: 0.001}}}
	unpriced := &uniai.ChatResult{Text: "modelcheck-ok", Model: "claude-haiku-5-5-20261101"}

	// Reuses earlier responses: no extra request.
	f := &fakeChat{results: []*uniai.ChatResult{priced}}
	r := newRunner(f, "claude-haiku-5-5")
	r.run(context.Background(), caseText)
	res := r.run(context.Background(), casePricing)
	if res.Status != statusPass || len(f.requests) != 1 || !strings.Contains(res.Detail, "$0.1 in / $0.01 cached / $0.5 out per 1M; 1 responses costed") {
		t.Fatalf("priced: %#v (requests %d)", res, len(f.requests))
	}

	// Pricing alone makes one probe request.
	f = &fakeChat{results: []*uniai.ChatResult{priced}}
	if res := newRunner(f, "claude-haiku-5-5").run(context.Background(), casePricing); res.Status != statusPass || res.Requests != 1 || len(f.requests) != 1 {
		t.Fatalf("probe: %#v", res)
	}

	// Reported model name without a price fails even though the request model is priced.
	f = &fakeChat{results: []*uniai.ChatResult{unpriced}}
	r = newRunner(f, "claude-haiku-5-5")
	r.run(context.Background(), caseText)
	r.run(context.Background(), caseTool)
	res = r.run(context.Background(), casePricing)
	if res.Status != statusFail || strings.Contains(res.Detail, "no catalog price") ||
		!strings.Contains(res.Detail, `text (reported model "claude-haiku-5-5-20261101"), tool`) {
		t.Fatalf("reported model: %#v", res)
	}

	// Unknown model fails the static check too.
	f = &fakeChat{results: []*uniai.ChatResult{{Text: "ok"}}}
	if res := newRunner(f, "brand-new-model").run(context.Background(), casePricing); res.Status != statusFail || !strings.Contains(res.Detail, `no catalog price for "brand-new-model"`) {
		t.Fatalf("unknown model: %#v", res)
	}

	// A cache hit on a model without a cached-input rate gets no cost; say why.
	noCached, err := uniai.ParsePricingYAML([]byte("chat:\n  - model: no-cache-rate\n    input_usd_per_million: 1\n    output_usd_per_million: 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if info, ok := catalogPrice(noCached, "no-cache-rate"); !ok || info.cached != nil || info.String() != "$1 in / no cached rate / $2 out per 1M" {
		t.Fatalf("catalogPrice: %#v %v", info, ok)
	}
	f = &fakeChat{results: []*uniai.ChatResult{{Text: "ok", Usage: uniai.Usage{InputTokens: 9000, Cache: uniai.UsageCache{CachedInputTokens: 8000}}}}}
	r = &runner{chat: f.chat, model: modelConfig{Provider: "openai", Model: "no-cache-rate"}, timeout: 1e9, catalog: noCached}
	r.run(context.Background(), caseText)
	if res := r.run(context.Background(), casePricing); res.Status != statusFail || !strings.Contains(res.Detail, "text (catalog has no cached-input rate)") {
		t.Fatalf("missing cached rate: %#v", res)
	}

	if got := strings.Join(modelCases(modelConfig{}, []string{"pricing", "text", "tool"}), ","); got != "text,tool,pricing" {
		t.Fatalf("pricing should run last: %s", got)
	}
}

func TestRunListAndFilters(t *testing.T) {
	var out bytes.Buffer
	failed, err := run(context.Background(), []string{"--list", "--only", "kimi,deepseek-flash"}, &out, envMap(map[string]string{"DEEPSEEK_API_KEY": "k"}))
	if err != nil || failed {
		t.Fatalf("list: failed=%v err=%v", failed, err)
	}
	got := out.String()
	if !strings.Contains(got, "kimi-k3.1-pro") || !strings.Contains(got, "https://api.moonshot.ai/v1") || !strings.Contains(got, "SKIP: env MOONSHOT_API_KEY is empty") ||
		!strings.Contains(got, "deepseek-flash") || strings.Contains(got, "deepseek-v4-pro") || strings.Contains(got, "gpt-6") ||
		!strings.Contains(got, "$0.15 in / $") {
		t.Fatalf("unexpected list output:\n%s", got)
	}

	// All models skipped: no API calls, no failure.
	out.Reset()
	failed, err = run(context.Background(), []string{"--only", "gemini-4-argon", "--cases", "text,tool"}, &out, envMap(nil))
	if err != nil || failed || !strings.Contains(out.String(), "Skipped (missing credentials): gemini/gemini-4-argon") {
		t.Fatalf("skip run: failed=%v err=%v\n%s", failed, err, out.String())
	}

	if _, err := run(context.Background(), []string{"--cases", "speed"}, &out, envMap(nil)); err == nil {
		t.Fatal("expected unknown case error")
	}
	if got := modelCases(modelConfig{modelSettings: modelSettings{Cases: []string{"tool", "text"}}}, []string{"text", "json", "tool"}); strings.Join(got, ",") != "text,tool" {
		t.Fatalf("modelCases = %v", got)
	}
}

func TestColorOutput(t *testing.T) {
	// Buffers are not terminals: auto mode stays plain.
	if colorEnabled("auto", &bytes.Buffer{}, envMap(nil)) || !colorEnabled("always", &bytes.Buffer{}, envMap(map[string]string{"NO_COLOR": "1"})) ||
		colorEnabled("never", os.Stdout, envMap(nil)) || colorEnabled("auto", os.Stdout, envMap(map[string]string{"NO_COLOR": "1"})) ||
		colorEnabled("auto", os.Stdout, envMap(map[string]string{"TERM": "dumb"})) {
		t.Fatal("wrong color detection")
	}
	if _, err := parseFlags([]string{"--color", "rainbow"}); err == nil {
		t.Fatal("expected invalid --color error")
	}

	results := []modelResult{
		{Name: "openai/gpt-6-luna", Provider: "openai_resp", Cases: []caseResult{{Case: caseText, Status: statusPass}, {Case: caseTool, Status: statusFail, Detail: "no tool call returned"}}},
		{Name: "gemini/x", Provider: "gemini", Skip: "env GEMINI_API_KEY is empty", Cases: []caseResult{{Case: caseText, Status: statusSkip}, {Case: caseTool, Status: statusSkip}}},
	}
	var colored, plain bytes.Buffer
	for _, tt := range []struct {
		out *bytes.Buffer
		p   painter
	}{{&colored, painter{true}}, {&plain, painter{false}}} {
		printTable(tt.out, tt.p, results, []string{caseText, caseTool})
		printDetails(tt.out, tt.p, results, false)
		printSummary(tt.out, tt.p, results)
	}
	got := colored.String()
	for _, want := range []string{"\x1b[32mPASS 0.0s\x1b[0m", "\x1b[31;1mFAIL\x1b[0m", "\x1b[33mSKIP\x1b[0m", "\x1b[32m1 passed\x1b[0m", "\x1b[31;1m1 failed\x1b[0m"} {
		if !strings.Contains(got, want) {
			t.Errorf("colored output missing %q:\n%q", want, got)
		}
	}
	if strings.Contains(plain.String(), "\x1b[") {
		t.Fatalf("plain output has escape codes: %q", plain.String())
	}
	// Stripping the codes must give exactly the plain output, so padding is
	// computed on visible text and columns stay aligned.
	if stripped := regexp.MustCompile("\x1b\\[[0-9;]*m").ReplaceAllString(got, ""); stripped != plain.String() {
		t.Fatalf("colored layout differs from plain:\n%s\n---\n%s", stripped, plain.String())
	}
	if !strings.Contains(plain.String(), "openai/gpt-6-luna  openai_resp  PASS 0.0s  FAIL  -") {
		t.Fatalf("table not aligned:\n%s", plain.String())
	}
}
