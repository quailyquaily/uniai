package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai"
)

func TestRunReceivesReasoningThroughStreamCallback(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-secret" {
			t.Errorf("unexpected authorization header: %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(w, `{"id":"chatcmpl_test","object":"chat.completion.chunk","created":0,"model":"kimi-k3","choices":[{"index":0,"delta":{"reasoning_content":"inspect"},"finish_reason":null}]}`)
		writeSSE(w, `{"id":"chatcmpl_test","object":"chat.completion.chunk","created":0,"model":"kimi-k3","choices":[{"index":0,"delta":{"reasoning_content":" first"},"finish_reason":null}]}`)
		writeSSE(w, `{"id":"chatcmpl_test","object":"chat.completion.chunk","created":0,"model":"kimi-k3","choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":null}]}`)
		writeSSE(w, `{"id":"chatcmpl_test","object":"chat.completion.chunk","created":0,"model":"kimi-k3","choices":[{"index":0,"delta":{"content":"."},"finish_reason":null}]}`)
		writeSSE(w, `{"id":"chatcmpl_test","object":"chat.completion.chunk","created":0,"model":"kimi-k3","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		writeSSE(w, `{"id":"chatcmpl_test","object":"chat.completion.chunk","created":0,"model":"kimi-k3","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4}}`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	t.Setenv("STREAM_TEST_API_KEY", "test-secret")
	configPath := writeTestConfig(t, fmt.Sprintf(`
tests:
  - name: kimi
    provider: openai
    api_base: %s/v1
    api_key_ref: STREAM_TEST_API_KEY
    model: kimi-k3
`, server.URL))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run([]string{"--config", configPath, "run", "kimi"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}

	if requestBody["stream"] != true {
		t.Fatalf("request did not enable streaming: %#v", requestBody)
	}
	if requestBody["model"] != "kimi-k3" {
		t.Fatalf("unexpected request model: %#v", requestBody["model"])
	}

	out := stdout.String()
	headerAt := strings.Index(out, "kimi (openai / kimi-k3)")
	reasoningAt := strings.Index(out, "Reasoning:\ninspect first")
	answerAt := strings.Index(out, "Answer:\nanswer.")
	usageAt := strings.Index(out, "Usage: input 2, output 2, total 4 tokens")
	passAt := strings.Index(out, "PASS: reasoning 13 chars (2 chunks), answer 7 chars (2 chunks), elapsed ")
	if headerAt < 0 || reasoningAt < 0 || answerAt < 0 || usageAt < 0 || passAt < 0 {
		t.Fatalf("missing stream output:\n%s", out)
	}
	if !(headerAt < reasoningAt && reasoningAt < answerAt && answerAt < usageAt && usageAt < passAt) {
		t.Fatalf("stream events were printed out of order:\n%s", out)
	}
	if strings.Contains(out, "delta=") {
		t.Fatalf("output exposes debug-style chunk formatting:\n%s", out)
	}
	if strings.Count(out, "Reasoning:") != 1 || strings.Count(out, "Answer:") != 1 {
		t.Fatalf("section headings were repeated for stream chunks:\n%s", out)
	}
}

func TestRunFailsWhenStreamHasNoReasoningDelta(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(w, `{"id":"chatcmpl_test","object":"chat.completion.chunk","created":0,"model":"kimi-k3","choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":null}]}`)
		writeSSE(w, `{"id":"chatcmpl_test","object":"chat.completion.chunk","created":0,"model":"kimi-k3","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	t.Setenv("STREAM_TEST_API_KEY", "test-secret")
	configPath := writeTestConfig(t, fmt.Sprintf(`
tests:
  - name: no_reasoning
    provider: openai
    api_base: %s/v1
    api_key_ref: STREAM_TEST_API_KEY
    model: kimi-k3
`, server.URL))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run([]string{"--config", configPath, "run", "no_reasoning"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "no non-empty reasoning delta") {
		t.Fatalf("expected missing reasoning failure, got %v\nstdout: %s", err, stdout.String())
	}
	if !strings.Contains(stdout.String(), "FAIL") {
		t.Fatalf("failure was not reported:\n%s", stdout.String())
	}
}

func TestLoadConfigRejectsConflictingReasoningControls(t *testing.T) {
	configPath := writeTestConfig(t, `
tests:
  - name: invalid
    provider: anthropic
    api_key_ref: ANTHROPIC_API_KEY
    model: claude-sonnet-4-5
    reasoning_effort: high
    reasoning_budget_tokens: 4096
`)

	_, err := loadConfig(configPath)
	if err == nil || !strings.Contains(err.Error(), "reasoning_effort and reasoning_budget_tokens cannot be used together") {
		t.Fatalf("expected conflicting reasoning controls error, got %v", err)
	}
}

func TestBuildClientConfigUsesReferencedEnvironmentKey(t *testing.T) {
	t.Setenv("PROVIDER_API_KEY", "provider-secret")

	tests := []struct {
		name     string
		provider string
		assert   func(*testing.T, uniai.Config)
	}{
		{
			name:     "openai compatible",
			provider: "openai",
			assert: func(t *testing.T, cfg uniai.Config) {
				if cfg.OpenAIAPIKey != "provider-secret" || cfg.OpenAIModel != "model" {
					t.Fatalf("unexpected OpenAI config: %#v", cfg)
				}
			},
		},
		{
			name:     "deepseek",
			provider: "deepseek",
			assert: func(t *testing.T, cfg uniai.Config) {
				if cfg.OpenAIAPIKey != "provider-secret" || cfg.Provider != "deepseek" {
					t.Fatalf("unexpected DeepSeek config: %#v", cfg)
				}
			},
		},
		{
			name:     "openai codex",
			provider: "openai_codex",
			assert: func(t *testing.T, cfg uniai.Config) {
				if cfg.OpenAIAPIKey != "provider-secret" || cfg.OpenAIModel != "model" || cfg.Provider != "openai_codex" {
					t.Fatalf("unexpected OpenAI Codex config: %#v", cfg)
				}
			},
		},
		{
			name:     "gemini",
			provider: "gemini",
			assert: func(t *testing.T, cfg uniai.Config) {
				if cfg.GeminiAPIKey != "provider-secret" || cfg.GeminiModel != "model" {
					t.Fatalf("unexpected Gemini config: %#v", cfg)
				}
			},
		},
		{
			name:     "anthropic",
			provider: "anthropic",
			assert: func(t *testing.T, cfg uniai.Config) {
				if cfg.AnthropicAPIKey != "provider-secret" || cfg.AnthropicModel != "model" {
					t.Fatalf("unexpected Anthropic config: %#v", cfg)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := buildClientConfig(testConfig{
				Provider:  tc.provider,
				APIBase:   "https://example.test/v1",
				APIKeyRef: "PROVIDER_API_KEY",
				Model:     "model",
			})
			if err != nil {
				t.Fatalf("build client config: %v", err)
			}
			tc.assert(t, cfg)
		})
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	configPath := writeTestConfig(t, `
tests:
  - name: invalid
    provider: openai
    api_key_ref: OPENAI_API_KEY
    model: kimi-k3
    typo_field: true
`)

	if _, err := loadConfig(configPath); err == nil || !strings.Contains(err.Error(), "field typo_field not found") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestExampleConfigIncludesRequestedReasoningModels(t *testing.T) {
	cfg, err := loadConfig("config.example.yaml")
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}

	want := map[string]struct {
		provider  string
		keyRef    string
		maxTokens int
		effort    string
	}{
		"claude-sonnet-5": {
			provider:  "anthropic",
			keyRef:    "ANTHROPIC_API_KEY",
			maxTokens: 8192,
			effort:    "high",
		},
		"gpt-5.6-luna": {
			provider:  "openai_resp",
			keyRef:    "OPENAI_API_KEY",
			maxTokens: 32768,
			effort:    "high",
		},
	}

	for _, test := range cfg.Tests {
		expected, ok := want[test.Model]
		if !ok {
			continue
		}
		if test.Provider != expected.provider || test.APIKeyRef != expected.keyRef || test.MaxTokens != expected.maxTokens {
			t.Fatalf("unexpected config for %s: %#v", test.Model, test)
		}
		if test.ReasoningEffort != expected.effort {
			t.Fatalf("expected %s reasoning effort %s, got %q", test.Model, expected.effort, test.ReasoningEffort)
		}
		delete(want, test.Model)
	}
	if len(want) != 0 {
		t.Fatalf("missing requested model configs: %#v", want)
	}
}

func writeSSE(w http.ResponseWriter, payload string) {
	fmt.Fprintf(w, "data: %s\n\n", payload)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeTestConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(contents)+"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
