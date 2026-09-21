package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/evaluate"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestCLIEnvironmentAndOverrides(t *testing.T) {
	getenv := env(map[string]string{"EVALUATE_PROVIDER": "openai", "EVALUATE_MODEL": "Qwen/test", "OPENAI_API_KEY": "private-key", "OPENAI_API_BASE": "https://llm.example/v1", "EVALUATE_REASONING_EFFORT": "low", "EVALUATE_MAX_TOKENS": "256"})
	o, err := parseOptions([]string{"--model", "override", "--reasoning-effort", "none"}, getenv, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if o.request.Provider != "openai" || o.request.Model != "override" || o.request.EmulationMode != evaluate.EmulationForce || *o.request.EmulationOptions.MaxTokens != 256 || *o.request.EmulationOptions.ReasoningEffort != chat.ReasoningEffortNone {
		t.Fatal(o)
	}
	cfg, err := clientConfig(o, getenv)
	if err != nil || cfg.OpenAIAPIKey != "private-key" || cfg.OpenAIAPIBase != "https://llm.example/v1" {
		t.Fatalf("configuration mismatch: %v", err)
	}
	if _, err := clientConfig(o, env(nil)); err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--repeat", "0"}, {"--warmup", "-1"}, {"--limit", "-1"}, {"--timeout", "0s"}, {"--max-tokens", "0"}, {"--max-tokens", "bad"}, {"--boolean-threshold", "NaN"}, {"--score-tolerance", "-1"}, {"--mode", "unknown"}, {"--reasoning-effort", "bogus"}, {"--provider", "typesafe", "--max-tokens", "64"}, {"--provider", "typesafe", "--mode", "force"}, {"--provider", "openai", "--mode", "off", "--model", "test"}, {"unexpected"}} {
		if _, err := parseOptions(args, env(nil), &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestListAndDryRunNeedNoCredentials(t *testing.T) {
	for _, args := range [][]string{{"--list", "--limit", "3"}, {"--list", "--domain", "crypto", "--language", "ja", "--limit", "2"}, {"--dry-run", "--limit", "2", "--repeat", "3", "--warmup", "1"}} {
		var stdout, stderr bytes.Buffer
		if err := run(context.Background(), args, env(nil), &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		if stdout.Len() == 0 {
			t.Fatal("no output")
		}
		if strings.Contains(strings.Join(args, " "), "--dry-run") && !strings.Contains(stdout.String(), "requests=7") {
			t.Fatal(stdout.String())
		}
	}
	var out bytes.Buffer
	for _, args := range [][]string{{"--list", "--category", "nonexistent"}, {"--list", "--domain", "unknown-domain"}, {"--list", "--language", "unknown-language"}} {
		if err := run(context.Background(), args, env(nil), &out, &out); err == nil {
			t.Fatal("empty selection accepted:", args)
		}
	}
}

func TestCLINativeRunAndReport(t *testing.T) {
	dir := t.TempDir()
	casePath := filepath.Join(dir, "input.jsonl")
	output := filepath.Join(dir, "report.json")
	data := `{"id":"false-zero","category":"smoke","domain":"general","language":"en","state":"The request was withdrawn.","questions":{"answer":{"kind":"boolean","instructions":"Current refund request?"}},"expected":{"answer":false}}`
	if err := os.WriteFile(casePath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("wrong native target/credentials")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if _, ok := payload["expected"]; ok {
			t.Error("gold labels sent")
		}
		fmt.Fprint(w, `{"model":"jev-1.13.0","answers":{"answer":{"type":"noul","noul":0}},"usage":{"input_tokens":20,"output_tokens":1}}`)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--cases", casePath, "--output", output, "--repeat", "2"}, env(map[string]string{"TYPESAFE_API_KEY": "test-secret", "TYPESAFE_API_BASE": server.URL + "/v1"}), &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !strings.Contains(stdout.String(), "false-zero") || !strings.Contains(stdout.String(), "false") || strings.Contains(stdout.String(), "test-secret") {
		t.Fatal(stdout.String())
	}
	var report benchmarkReport
	encoded, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &report); err != nil {
		t.Fatal(err)
	}
	if report.Summary.MatchedJudgments != 2 || len(report.DatasetSHA256) != 64 || report.Provider != "typesafe" || report.Attempts[0].Result.Answers["answer"].ProbabilityTrue == nil {
		t.Fatal(report)
	}
	if strings.Contains(string(encoded), "test-secret") || strings.Contains(string(encoded), casePath) {
		t.Fatal("report leaked credentials or machine path")
	}
	// Refuse to overwrite either an existing report or the source dataset.
	if err := run(context.Background(), []string{"--cases", casePath, "--output", casePath}, env(map[string]string{"TYPESAFE_API_KEY": "test-secret"}), &stdout, &stderr); err == nil || calls != 2 {
		t.Fatal("overwrote input or sent a request before checking output")
	}
}

func TestCaseSelectionDeterministic(t *testing.T) {
	cases, err := loadCases(strings.NewReader(builtinCases))
	if err != nil {
		t.Fatal(err)
	}
	one, err := selectCases(cases, "", "", "", 12, 42)
	if err != nil {
		t.Fatal(err)
	}
	two, _ := selectCases(cases, "", "", "", 12, 42)
	for i := range one {
		if one[i].ID != two[i].ID {
			t.Fatal("seed not reproducible")
		}
	}
	if one[0].ID == cases[0].ID {
		t.Fatal("shuffle did not take effect")
	}
	subset, err := selectCases(cases, "refund_intent", "", "", 0, 0)
	if err != nil || len(subset) != 30 {
		t.Fatal(err, len(subset))
	}
	japaneseCrypto, err := selectCases(cases, "", "crypto", "ja", 0, 0)
	if err != nil || len(japaneseCrypto) != 8 {
		t.Fatal(err, len(japaneseCrypto))
	}
}

func TestCLIEmulationOptionsAndPartialFailureReport(t *testing.T) {
	dir := t.TempDir()
	casePath := filepath.Join(dir, "cases.jsonl")
	output := filepath.Join(dir, "report.json")
	if err := os.WriteFile(casePath, []byte(testCaseJSON), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/v1/chat/completions" || body["model"] != "Qwen/example" || body["reasoning_effort"] != "none" || body["max_tokens"] != float64(512) {
			t.Errorf("unexpected Qwen request: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		content := `{"refund":false,"team":"billing","priority":0}`
		if calls == 2 {
			content = "invalid JSON"
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "served-qwen", "choices": []any{map[string]any{"message": map[string]any{"content": content}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--cases", casePath, "--repeat", "2", "--output", output}, env(map[string]string{"EVALUATE_PROVIDER": "openai", "EVALUATE_MODEL": "Qwen/example", "OPENAI_API_KEY": "key", "OPENAI_API_BASE": server.URL + "/v1", "EVALUATE_REASONING_EFFORT": "none", "EVALUATE_MAX_TOKENS": "512"}), &stdout, &stderr)
	if err == nil || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	data, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var report benchmarkReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Summary.Succeeded != 1 || report.Summary.Failed != 1 || !report.Attempts[0].Result.Emulated || report.Attempts[1].Error == "" || report.EmulationOptions == nil || *report.EmulationOptions.MaxTokens != 512 {
		t.Fatal(report)
	}
}
