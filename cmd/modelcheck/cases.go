package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai"
	"github.com/quailyquaily/uniai/internal/modelcompat"
)

const (
	caseText  = "text"
	caseJSON  = "json"
	caseCache = "cache"
	caseTool  = "tool"
	// casePricing runs last and reuses the responses from earlier cases.
	casePricing = "pricing"

	textToken = "modelcheck-ok"
)

var allCases = []string{caseText, caseJSON, caseCache, caseTool, casePricing}

func isKnownCase(name string) bool {
	for _, c := range allCases {
		if c == name {
			return true
		}
	}
	return false
}

type status string

const (
	statusPass status = "PASS"
	statusFail status = "FAIL"
	statusSkip status = "SKIP"
)

type caseResult struct {
	Case     string        `json:"case"`
	Status   status        `json:"status"`
	Duration time.Duration `json:"duration_ns"`
	Detail   string        `json:"detail,omitempty"`
	Response string        `json:"response,omitempty"`
	Usage    *uniai.Usage  `json:"usage,omitempty"`
	// Cache case only: usage of the cold request and of the request that hit.
	ColdUsage *uniai.Usage `json:"cold_usage,omitempty"`
	Requests  int          `json:"requests"`
	// CostUSD sums the estimated cost of every request in the case; Priced is
	// false when the pricing catalog has no rate for the model.
	CostUSD float64 `json:"cost_usd"`
	Priced  bool    `json:"priced"`
	// CacheHitRate is cached / input tokens of the last repeat (cache case).
	CacheHitRate *float64 `json:"cache_hit_rate,omitempty"`
}

// chatFunc matches uniai.Client.Chat so cases can be tested offline.
type chatFunc func(ctx context.Context, opts ...uniai.ChatOption) (*uniai.ChatResult, error)

type runner struct {
	chat          chatFunc
	model         modelConfig
	apiBase       string
	maxTokens     int
	timeout       time.Duration
	cacheAttempts int
	cacheDelay    time.Duration
	nonce         string
	catalog       *uniai.PricingCatalog

	current string
	cost    float64
	priced  bool

	// observed records every successful response for the pricing case.
	observed []observation
}

type observation struct {
	caseName     string
	model        string // model name reported by the API, used for blocking cost
	priced       bool
	cachedTokens int
}

func (r *runner) baseOptions() []uniai.ChatOption {
	opts := []uniai.ChatOption{uniai.WithProvider(r.model.Provider), uniai.WithModel(r.model.Model)}
	if r.maxTokens > 0 {
		opts = append(opts, uniai.WithMaxTokens(r.maxTokens))
	}
	if effort := strings.TrimSpace(r.model.ReasoningEffort); effort != "" {
		opts = append(opts, uniai.WithReasoningEffort(uniai.ReasoningEffort(effort)))
	}
	return opts
}

func (r *runner) call(ctx context.Context, opts ...uniai.ChatOption) (*uniai.ChatResult, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	start := time.Now()
	resp, err := r.chat(ctx, append(r.baseOptions(), opts...)...)
	elapsed := time.Since(start)
	if err == nil && resp == nil {
		err = fmt.Errorf("empty response")
	}
	if err == nil {
		r.observed = append(r.observed, observation{caseName: r.current, model: resp.Model, priced: resp.Usage.Cost != nil, cachedTokens: resp.Usage.Cache.CachedInputTokens})
		if resp.Usage.Cost != nil {
			r.cost += resp.Usage.Cost.Total
			r.priced = true
		}
	}
	return resp, elapsed, err
}

func (r *runner) run(ctx context.Context, name string) caseResult {
	r.cost, r.priced, r.current = 0, false, name
	var res caseResult
	switch name {
	case caseText:
		res = r.runText(ctx)
	case caseJSON:
		res = r.runJSON(ctx)
	case caseCache:
		res = r.runCache(ctx)
	case caseTool:
		res = r.runTool(ctx)
	case casePricing:
		res = r.runPricing(ctx)
	default:
		res = caseResult{Case: name, Status: statusFail, Detail: "unknown case"}
	}
	res.CostUSD, res.Priced = r.cost, r.priced
	return res
}

// 1. Basic text request.
func (r *runner) runText(ctx context.Context) caseResult {
	res := caseResult{Case: caseText, Requests: 1}
	resp, d, err := r.call(ctx, uniai.WithMessages(uniai.User("Reply with exactly this text and nothing else: "+textToken)))
	res.Duration = d
	if err != nil {
		return failed(res, err)
	}
	res.Response, res.Usage = resp.Text, &resp.Usage
	if !strings.Contains(resp.Text, textToken) {
		res.Status, res.Detail = statusFail, fmt.Sprintf("reply does not contain %q", textToken)
		return res
	}
	res.Status = statusPass
	return res
}

const jsonPrompt = `Return a JSON object describing Tokyo with exactly these keys:
"city" (string, "Tokyo"), "country" (string, "Japan"), and "population_millions" (number).
Output only the JSON object.`

// 2. Basic JSON request.
func (r *runner) runJSON(ctx context.Context) caseResult {
	res := caseResult{Case: caseJSON, Requests: 1}
	mode, opt, err := r.jsonOption()
	if err != nil {
		res.Status, res.Detail = statusFail, err.Error()
		return res
	}
	opts := []uniai.ChatOption{uniai.WithMessages(uniai.User(jsonPrompt))}
	if opt != nil {
		opts = append(opts, opt)
	}
	resp, d, err := r.call(ctx, opts...)
	res.Duration = d
	if err != nil {
		return failed(res, fmt.Errorf("json_mode=%s: %w", mode, err))
	}
	res.Response, res.Usage = resp.Text, &resp.Usage
	if err := checkJSONReply(resp.Text); err != nil {
		res.Status, res.Detail = statusFail, fmt.Sprintf("json_mode=%s: %v", mode, err)
		return res
	}
	res.Status, res.Detail = statusPass, "json_mode="+mode
	return res
}

// jsonOption returns the provider-native JSON switch, or nil for prompt-only.
func (r *runner) jsonOption() (string, uniai.ChatOption, error) {
	mode := r.model.JSONMode
	if mode == "" {
		mode = "auto"
	}
	if mode == "prompt" {
		return mode, nil, nil
	}
	switch r.model.Provider {
	case "gemini":
		return "native", uniai.WithOpenAIOptions(structs.JSONMap{"response_mime_type": "application/json"}), nil
	case "anthropic":
		if mode == "native" {
			return mode, nil, fmt.Errorf("provider anthropic has no native JSON mode; use json_mode: prompt")
		}
		return "prompt", nil, nil
	default:
		return "native", uniai.WithOpenAIOptions(structs.JSONMap{"response_format": map[string]any{"type": "json_object"}}), nil
	}
}

func checkJSONReply(text string) error {
	raw := extractJSONObject(text)
	if raw == "" {
		return fmt.Errorf("no JSON object in reply")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return fmt.Errorf("invalid JSON: %v", err)
	}
	if city, _ := obj["city"].(string); !strings.EqualFold(city, "Tokyo") {
		return fmt.Errorf("city = %v, want Tokyo", obj["city"])
	}
	if country, _ := obj["country"].(string); !strings.EqualFold(country, "Japan") {
		return fmt.Errorf("country = %v, want Japan", obj["country"])
	}
	if _, ok := obj["population_millions"].(float64); !ok {
		return fmt.Errorf("population_millions = %v, want a number", obj["population_millions"])
	}
	return nil
}

// extractJSONObject tolerates code fences and surrounding prose.
func extractJSONObject(text string) string {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return ""
	}
	return text[start : end+1]
}

// 3. Two large identical requests; the second must report cached input.
func (r *runner) runCache(ctx context.Context) caseResult {
	res := caseResult{Case: caseCache}
	breakpoint, extra := r.cacheOptions()
	prefix := uniai.TextPart(cachePrefix(r.nonce))
	if breakpoint != nil {
		prefix = uniai.WithPartCacheControl(prefix, *breakpoint)
	}
	opts := []uniai.ChatOption{uniai.WithMessages(uniai.UserParts(prefix, uniai.TextPart("\n\nReply with exactly: cache-ok")))}
	if extra != nil {
		opts = append(opts, extra)
	}

	cold, d, err := r.call(ctx, opts...)
	res.Duration += d
	res.Requests++
	if err != nil {
		return failed(res, fmt.Errorf("cold request: %w", err))
	}
	res.ColdUsage = &cold.Usage

	for attempt := 1; attempt <= r.cacheAttempts; attempt++ {
		if err := sleep(ctx, r.cacheDelay); err != nil {
			return failed(res, err)
		}
		warm, d, err := r.call(ctx, opts...)
		res.Duration += d
		res.Requests++
		if err != nil {
			return failed(res, fmt.Errorf("repeat request %d: %w", attempt, err))
		}
		res.Usage, res.Response = &warm.Usage, warm.Text
		rate := hitRate(warm.Usage)
		res.CacheHitRate = &rate
		if warm.Usage.Cache.CachedInputTokens > 0 {
			res.Status = statusPass
			res.Detail = fmt.Sprintf("hit rate %.1f%% on repeat %d: cached %d of %d input tokens%s%s",
				rate*100, attempt, warm.Usage.Cache.CachedInputTokens, warm.Usage.InputTokens,
				coldWriteNote(cold.Usage), cacheModeNote(breakpoint))
			return res
		}
	}
	res.Status = statusFail
	res.Detail = fmt.Sprintf("hit rate 0%% after %d repeats (cold input %d tokens)%s%s",
		r.cacheAttempts, cold.Usage.InputTokens, coldWriteNote(cold.Usage), cacheModeNote(breakpoint))
	return res
}

// cacheOptions picks an explicit breakpoint and request-level cache options.
// auto: Anthropic gets a 5m breakpoint; GPT-5.6/GPT-6 on api.openai.com get
// OpenAI's boundary marker. Everything else relies on implicit caching.
func (r *runner) cacheOptions() (*uniai.CacheControl, uniai.ChatOption) {
	provider, mode := r.model.Provider, r.model.CacheBreakpoint
	officialOpenAI := (provider == "openai" || provider == "openai_resp") && modelcompat.IsOfficialOpenAIBase(r.apiBase)

	var extra uniai.ChatOption
	if officialOpenAI {
		extra = uniai.WithOpenAIOptions(structs.JSONMap{"prompt_cache_key": "uniai-modelcheck-" + r.nonce})
	}

	use := mode == "on"
	if mode == "" || mode == "auto" {
		use = provider == "anthropic" || (officialOpenAI && modelcompat.OpenAIUsesPromptCacheOptions(r.model.Model))
	}
	if !use {
		return nil, extra
	}
	ctrl := uniai.CacheControl{} // OpenAI boundary marker; TTL is request-wide.
	if provider == "anthropic" {
		ctrl = uniai.CacheTTL5m()
	}
	return &ctrl, extra
}

// hitRate is the share of input tokens served from cache.
func hitRate(u uniai.Usage) float64 {
	if u.InputTokens <= 0 {
		return 0
	}
	return float64(u.Cache.CachedInputTokens) / float64(u.InputTokens)
}

func coldWriteNote(u uniai.Usage) string {
	if u.Cache.CacheCreationInputTokens <= 0 {
		return ""
	}
	return fmt.Sprintf("; cold request wrote %d", u.Cache.CacheCreationInputTokens)
}

func cacheModeNote(ctrl *uniai.CacheControl) string {
	if ctrl == nil {
		return " [implicit]"
	}
	if ctrl.TTL != "" {
		return " [breakpoint ttl=" + ctrl.TTL + "]"
	}
	return " [breakpoint]"
}

// cachePrefix is roughly 9k tokens: above every provider's minimum cacheable
// prompt (Gemini 4 and some Claude models need 4,096). The nonce makes the
// first request of each run a cold write.
func cachePrefix(nonce string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Reference document %s. Treat it as fixed context.\n", nonce)
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&b, "Section %03d: services record latency, error budgets, rollout stages, incident owners, cost limits, and API contract versions for audit.\n", i)
	}
	return b.String()
}

var weatherSchema = []byte(`{
	"type": "object",
	"properties": {
		"city": {"type": "string", "description": "City name, e.g. Tokyo"},
		"unit": {"type": "string", "enum": ["celsius", "fahrenheit"]}
	},
	"required": ["city"]
}`)

// 4. Basic tool calling with get_weather.
func (r *runner) runTool(ctx context.Context) caseResult {
	res := caseResult{Case: caseTool, Requests: 1}
	tools := []uniai.Tool{uniai.FunctionTool("get_weather", "Get the current weather for a city.", weatherSchema)}
	resp, d, err := r.call(ctx,
		uniai.WithMessages(uniai.User("What's the weather in Tokyo right now? Use the get_weather tool.")),
		uniai.WithTools(tools),
		uniai.WithToolChoice(uniai.ToolChoice{Mode: "auto"}),
	)
	res.Duration = d
	if err != nil {
		return failed(res, err)
	}
	res.Response, res.Usage = resp.Text, &resp.Usage
	if err := checkWeatherCall(resp.ToolCalls); err != nil {
		res.Status, res.Detail = statusFail, err.Error()
		return res
	}
	res.Status = statusPass
	return res
}

func checkWeatherCall(calls []uniai.ToolCall) error {
	if len(calls) == 0 {
		return fmt.Errorf("no tool call returned")
	}
	for _, call := range calls {
		if call.Function.Name != "get_weather" {
			continue
		}
		var args struct {
			City string `json:"city"`
		}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return fmt.Errorf("get_weather arguments are not JSON: %q", call.Function.Arguments)
		}
		if !strings.Contains(strings.ToLower(args.City), "tokyo") {
			return fmt.Errorf("get_weather city = %q, want Tokyo", args.City)
		}
		return nil
	}
	return fmt.Errorf("no get_weather call (got %s)", calls[0].Function.Name)
}

func failed(res caseResult, err error) caseResult {
	res.Status, res.Detail = statusFail, err.Error()
	return res
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// 5. Pricing: the catalog must price the configured model, and every response
// in this run must have received a cost estimate. Blocking responses are
// priced by the model name the API reports, which can differ from the request.
func (r *runner) runPricing(ctx context.Context) caseResult {
	res := caseResult{Case: casePricing}
	start := time.Now()
	price, staticOK := catalogPrice(r.catalog, r.model.Model)

	if len(r.observed) == 0 {
		// Pricing ran alone: make one small request to observe a response.
		resp, _, err := r.call(ctx, uniai.WithMessages(uniai.User("Reply with exactly this text and nothing else: "+textToken)))
		res.Requests = 1
		if err != nil {
			res.Duration = time.Since(start)
			return failed(res, fmt.Errorf("probe request: %w", err))
		}
		res.Usage = &resp.Usage
	}
	res.Duration = time.Since(start)

	var unpriced []string
	seen := map[string]bool{}
	for _, o := range r.observed {
		if o.priced {
			continue
		}
		label := o.model
		if label == "" {
			label = r.model.Model
		}
		reason := fmt.Sprintf("reported model %q", label)
		if o.cachedTokens > 0 && staticOK && price.cached == nil {
			// The estimator refuses cached tokens without a cached-input rate.
			reason = "catalog has no cached-input rate"
		}
		if key := o.caseName + ":" + reason; !seen[key] {
			seen[key] = true
			unpriced = append(unpriced, fmt.Sprintf("%s (%s)", o.caseName, reason))
		}
	}

	var problems []string
	if !staticOK {
		problems = append(problems, fmt.Sprintf("no catalog price for %q", r.model.Model))
	}
	if len(unpriced) > 0 {
		problems = append(problems, "responses without cost: "+strings.Join(unpriced, ", "))
	}
	if len(problems) > 0 {
		res.Status, res.Detail = statusFail, strings.Join(problems, "; ")
		return res
	}
	res.Status = statusPass
	res.Detail = fmt.Sprintf("%s; %d responses costed", price, len(r.observed))
	return res
}

// catalogPrice holds a model's small-prompt rates, in USD per 1M tokens.
type catalogPriceInfo struct {
	in, out float64
	cached  *float64 // nil when the catalog has no cached-input rate
}

func (p catalogPriceInfo) String() string {
	cached := "no cached rate"
	if p.cached != nil {
		cached = fmt.Sprintf("$%.4g cached", *p.cached)
	}
	return fmt.Sprintf("$%.4g in / %s / $%.4g out per 1M", p.in, cached, p.out)
}

func catalogPrice(catalog *uniai.PricingCatalog, model string) (catalogPriceInfo, bool) {
	var p catalogPriceInfo
	if catalog == nil {
		return p, false
	}
	cost, ok := catalog.EstimateChatCost(model, uniai.Usage{InputTokens: 1000, OutputTokens: 1000, TotalTokens: 2000})
	if !ok || cost == nil {
		return p, false
	}
	p.in, p.out = cost.Input*1000, cost.Output*1000
	cachedCost, ok := catalog.EstimateChatCost(model, uniai.Usage{InputTokens: 1000, TotalTokens: 1000,
		Cache: uniai.UsageCache{CachedInputTokens: 1000}})
	if ok && cachedCost != nil {
		rate := cachedCost.CachedInput * 1000
		p.cached = &rate
	}
	return p, true
}
