package uniai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientChatRoutesOpenAICodexToResponsesWithCompatibility(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		for _, key := range []string{"temperature", "max_output_tokens"} {
			if _, ok := payload[key]; ok {
				t.Fatalf("unexpected %s in payload: %#v", key, payload[key])
			}
		}
		reasoning, _ := payload["reasoning"].(map[string]any)
		if reasoning["effort"] != "high" {
			t.Fatalf("reasoning effort = %#v, want high", reasoning["effort"])
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"id":                  "resp_openai_codex_test",
			"object":              "response",
			"model":               "gpt-5.3-codex",
			"parallel_tool_calls": true,
			"status":              "completed",
			"output": []any{
				map[string]any{
					"id":     "msg_1",
					"type":   "message",
					"role":   "assistant",
					"status": "completed",
					"content": []any{
						map[string]any{
							"type":        "output_text",
							"text":        "ok",
							"annotations": []any{},
						},
					},
				},
			},
			"usage": map[string]any{
				"input_tokens":  1,
				"output_tokens": 1,
				"total_tokens":  2,
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client := New(Config{
		Provider:      "openai_codex",
		OpenAIAPIKey:  "test-key",
		OpenAIAPIBase: server.URL + "/v1",
		OpenAIModel:   "gpt-5.3-codex",
	})

	resp, err := client.Chat(context.Background(),
		WithMessages(User("hello")),
		WithTemperature(0.2),
		WithMaxTokens(512),
		WithReasoningBudgetTokens(4096),
		WithReasoningEffort(ReasoningEffortHigh),
	)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if resp.Text != "ok" {
		t.Fatalf("response text = %q, want ok", resp.Text)
	}
}
