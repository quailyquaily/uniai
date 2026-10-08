package openairesp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
)

func TestChatSessionCacheRouting(t *testing.T) {
	for _, codex := range []bool{false, true} {
		for _, mode := range []string{"blocking", "streaming", "stream_retry"} {
			name := "responses/" + mode
			if codex {
				name = "codex/" + mode
			}
			t.Run(name, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					if body["prompt_cache_key"] != "session-1" {
						t.Errorf("cache key = %#v", body["prompt_cache_key"])
					}
					for _, header := range []string{"session-id", "x-client-request-id"} {
						want := "configured"
						if codex {
							want = "session-1"
						}
						if got := r.Header.Get(header); got != want {
							t.Errorf("%s = %q, want %q", header, got, want)
						}
					}
					_, hasRetention := body["prompt_cache_retention"]
					if hasRetention == codex {
						t.Errorf("cache retention presence = %v, codex = %v", hasRetention, codex)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if mode == "stream_retry" && calls == 1 {
						return
					}
					writeOpenAIResponseSSE(t, w, map[string]any{
						"type": "response.completed", "sequence_number": 1,
						"response": map[string]any{"id": "resp_test", "object": "response", "status": "completed", "output": []any{}},
					})
				}))
				defer server.Close()
				p, err := New(Config{APIKey: "test", BaseURL: server.URL, DefaultModel: "gpt-5.5", OpenAICodex: codex,
					Headers: map[string]string{"session-id": "configured", "X-Client-Request-ID": "configured"},
				})
				if err != nil {
					t.Fatal(err)
				}
				req := &chat.Request{Messages: []chat.Message{chat.User("hello")}, Options: chat.Options{OpenAI: structs.JSONMap{
					"prompt_cache_key": " session-1 ", "prompt_cache_retention": "24h",
				}}}
				if mode == "streaming" {
					req.Options.OnStream = func(chat.StreamEvent) error { return nil }
				}
				if _, err := p.Chat(context.Background(), req); err != nil {
					t.Fatal(err)
				}
				wantCalls := 1
				if mode == "stream_retry" {
					wantCalls = 2
				}
				if calls != wantCalls {
					t.Fatalf("requests = %d, want %d", calls, wantCalls)
				}
			})
		}
	}
}

func TestBuildParamsCodexSessionCacheLength(t *testing.T) {
	for _, key := range []string{strings.Repeat("a", 64), strings.Repeat("a", 65), strings.Repeat("界", 64), strings.Repeat("界", 65)} {
		t.Run(key, func(t *testing.T) {
			_, err := buildParams(&chat.Request{Model: "gpt-5.4", Messages: []chat.Message{chat.User("hello")},
				Options: chat.Options{OpenAI: structs.JSONMap{"prompt_cache_key": key}},
			}, "", true)
			if len([]rune(key)) == 65 {
				if err == nil || !strings.Contains(err.Error(), "prompt_cache_key must not exceed 64 characters") {
					t.Fatalf("expected length error, got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
