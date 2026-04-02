package uniai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/quailyquaily/uniai/chat"
)

func TestClientChatAppliesCustomHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Test-Header"); got != "test-value" {
			t.Fatalf("expected custom header, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_test",
			"object":  "chat.completion",
			"created": 0,
			"model":   "gpt-4.1-mini",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": "ok",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     1,
				"completion_tokens": 1,
				"total_tokens":      2,
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client := New(Config{
		Provider:      "openai",
		OpenAIAPIKey:  "test-key",
		OpenAIAPIBase: server.URL + "/v1",
		OpenAIModel:   "gpt-4.1-mini",
		ChatHeaders: map[string]string{
			"X-Test-Header": "test-value",
		},
	})

	resp, err := client.Chat(context.Background(), chat.WithMessages(chat.User("hello")))
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if resp.Text != "ok" {
		t.Fatalf("unexpected text: %q", resp.Text)
	}
}
