package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestRunAgainstFakeGateway drives the real uniai client against a local
// OpenAI-compatible server, covering a custom gateway's key and base env vars.
func TestRunAgainstFakeGateway(t *testing.T) {
	var mu sync.Mutex
	cacheCalls := 0
	var sawJSONFormat, sawTools bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer abc-key" {
			http.Error(w, "bad key "+got, http.StatusUnauthorized)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		raw, _ := json.Marshal(body)
		mu.Lock()
		defer mu.Unlock()

		message := map[string]any{"role": "assistant", "content": "modelcheck-ok"}
		usage := map[string]any{"prompt_tokens": 20, "completion_tokens": 5, "total_tokens": 25}
		finish := "stop"
		switch {
		case body["tools"] != nil:
			sawTools = true
			finish = "tool_calls"
			message = map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{
				"id": "call_1", "type": "function",
				"function": map[string]any{"name": "get_weather", "arguments": `{"city":"Tokyo"}`},
			}}}
		case body["response_format"] != nil:
			sawJSONFormat = strings.Contains(string(raw), `"json_object"`)
			message["content"] = `{"city":"Tokyo","country":"Japan","population_millions":14}`
		case strings.Contains(string(raw), "Reference document"):
			cacheCalls++
			usage["prompt_tokens"] = 9000
			if cacheCalls > 1 {
				usage["prompt_tokens_details"] = map[string]any{"cached_tokens": 8960}
			}
			message["content"] = "cache-ok"
		}
		reported := body["model"]
		if reported == "gpt-6-sol" {
			reported = "gpt-6-sol-mystery-snapshot" // a name the catalog does not know
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "x", "object": "chat.completion", "created": 1, "model": reported,
			"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}},
			"usage":   usage,
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(`
cache_attempts: 2
providers:
  - name: abc # reads ABC_API_KEY and ABC_API_BASE
    models: [gpt-6-luna]
  - name: nope
    models: [other]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(dir, "report.json")
	env := envMap(map[string]string{"ABC_API_KEY": "abc-key", "ABC_API_BASE": srv.URL + "/v1"})

	var out bytes.Buffer
	failed, err := run(context.Background(), []string{"--config", cfgPath, "--json", report}, &out, env)
	if err != nil || failed {
		t.Fatalf("failed=%v err=%v\n%s", failed, err, out.String())
	}
	got := out.String()
	t.Log("\n" + got)
	for _, want := range []string{"abc/gpt-6-luna", "PASS", "hit rate 99.6% on repeat 1: cached 8960 of 9000", "[implicit]", "Skipped (missing credentials): nope/other"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "PASS 99.6% ") {
		t.Errorf("CACHE column should show the hit rate:\n%s", got)
	}
	if strings.Contains(got, "FAIL") {
		t.Errorf("unexpected failure:\n%s", got)
	}
	if !sawJSONFormat || !sawTools || cacheCalls != 2 {
		t.Errorf("server saw json_format=%v tools=%v cacheCalls=%d", sawJSONFormat, sawTools, cacheCalls)
	}

	var results []modelResult
	data, err := os.ReadFile(report)
	if err != nil || json.Unmarshal(data, &results) != nil || len(results) != 2 || len(results[0].Cases) != 5 {
		t.Fatalf("bad report: %v %s", err, data)
	}
	if results[0].KeyEnv != "ABC_API_KEY" || results[0].APIBase != srv.URL+"/v1" {
		t.Errorf("report credentials: %#v", results[0])
	}
	if p := results[0].Cases[4]; p.Case != casePricing || p.Status != statusPass || !strings.Contains(p.Detail, "$0.1 in / $0.01 cached / $0.5 out per 1M; 5 responses costed") {
		t.Errorf("pricing case: %#v", p)
	}

	// The configured model is priced, but the API reports a name the catalog
	// does not know, so responses get no cost and pricing fails.
	if err := os.WriteFile(cfgPath, []byte("providers: [{name: abc, models: [gpt-6-sol]}]"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	failed, err = run(context.Background(), []string{"--config", cfgPath, "--cases", "pricing"}, &out, env)
	if err != nil || !failed || !strings.Contains(out.String(), `responses without cost: pricing (reported model "gpt-6-sol-mystery-snapshot")`) ||
		strings.Contains(out.String(), "no catalog price") {
		t.Fatalf("expected reported-model pricing failure: failed=%v err=%v\n%s", failed, err, out.String())
	}
}
