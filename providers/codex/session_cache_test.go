package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/subscription"
)

func TestChatSessionCache(t *testing.T) {
	for _, model := range []string{"gpt-5.4", "gpt-5.5", "gpt-5.6"} {
		for _, configured := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/headers=%v", model, configured), func(t *testing.T) {
				headers := map[string]string{"X-Test": "preserved"}
				if configured {
					headers["session-id"] = "default-session"
					headers["X-Client-Request-ID"] = "default-request"
				}
				originalHeaders := maps.Clone(headers)
				var wantKey string
				calls := 0
				p, err := New(Config{CredentialSource: sessionCredentialSource{}, DefaultModel: model, Headers: headers,
					HTTPClient: testHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Fatal(err)
						}
						if wantKey == "" {
							if _, exists := body["prompt_cache_key"]; exists {
								t.Errorf("unexpected cache key: %#v", body["prompt_cache_key"])
							}
						} else if body["prompt_cache_key"] != wantKey {
							t.Errorf("cache key = %#v, want %q", body["prompt_cache_key"], wantKey)
						}
						for _, header := range []string{"session-id", "X-Client-Request-ID"} {
							want := wantKey
							if want == "" {
								want = originalHeaders[header]
							}
							if got := r.Header.Get(header); got != want {
								t.Errorf("%s = %q, want %q", header, got, want)
							}
						}
						if body["store"] != false {
							t.Errorf("store = %#v", body["store"])
						}
						for _, key := range []string{"prompt_cache_retention", "prompt_cache_options"} {
							if _, exists := body[key]; exists {
								t.Errorf("unexpected %s = %#v", key, body[key])
							}
						}
						if r.Header.Get("X-Test") != "preserved" {
							t.Error("custom header lost")
						}
						writeResponse(w, "ok")
					})),
				})
				if err != nil {
					t.Fatal(err)
				}
				// Reuse a session, switch sessions, then omit the key on the same provider.
				for _, key := range []string{"session-a", "session-a", "session-b", "", "   ", strings.Repeat("a", 64)} {
					wantKey = strings.TrimSpace(key)
					opts := structs.JSONMap{"store": true, "prompt_cache_retention": "24h", "prompt_cache_options": map[string]any{"ttl": "30m", "mode": "explicit"}}
					if key != "" {
						opts["prompt_cache_key"] = key
					}
					before, _ := json.Marshal(opts)
					req := &chat.Request{Messages: []chat.Message{chat.System("rules"), chat.User("hello")}, Options: chat.Options{OpenAI: opts}}
					for _, streaming := range []bool{false, true} {
						if streaming {
							req.Options.OnStream = func(chat.StreamEvent) error { return nil }
						}
						if _, err := p.Chat(context.Background(), req); err != nil {
							t.Fatal(err)
						}
					}
					after, _ := json.Marshal(opts)
					if string(before) != string(after) {
						t.Fatalf("options changed: %s -> %s", before, after)
					}
				}
				if calls != 12 {
					t.Fatalf("requests = %d, want 12", calls)
				}
				if !reflect.DeepEqual(headers, originalHeaders) {
					t.Fatalf("headers changed: %#v", headers)
				}
			})
		}
	}
}

func TestChatSessionCacheConcurrent(t *testing.T) {
	const sessions = 16
	var mu sync.Mutex
	counts := make(map[string]int)
	p, err := New(Config{CredentialSource: sessionCredentialSource{}, DefaultModel: "gpt-5.5",
		HTTPClient: testHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			// Instructions identify the intended session independently of cache fields.
			want, _ := body["instructions"].(string)
			if body["prompt_cache_key"] != want || r.Header.Get("session-id") != want || r.Header.Get("x-client-request-id") != want {
				t.Errorf("session %q: key=%#v, headers=%v", want, body["prompt_cache_key"], r.Header)
			}
			mu.Lock()
			counts[want]++
			mu.Unlock()
			writeResponse(w, "ok")
		})),
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			key := fmt.Sprintf("session-%d", i)
			req := &chat.Request{Messages: []chat.Message{chat.System(key), chat.User("hello")}, Options: chat.Options{OpenAI: structs.JSONMap{"prompt_cache_key": key}}}
			for round := 0; round < 2; round++ {
				if _, err := p.Chat(context.Background(), req); err != nil {
					t.Error(err)
					return
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 0; i < sessions; i++ {
		key := fmt.Sprintf("session-%d", i)
		if counts[key] != 2 {
			t.Errorf("%s: requests = %d, want 2", key, counts[key])
		}
	}
}

// Immutable credentials keep the concurrent test focused on request isolation.
type sessionCredentialSource struct{}

func (sessionCredentialSource) Credential(context.Context) (subscription.Credential, error) {
	return subscription.Credential{AccessToken: "test-access", AccountID: "test-account"}, nil
}

func (sessionCredentialSource) RefreshRejected(context.Context, string) (subscription.Credential, error) {
	return subscription.Credential{}, fmt.Errorf("unexpected credential refresh")
}
