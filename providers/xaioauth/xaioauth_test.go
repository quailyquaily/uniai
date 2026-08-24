package xaioauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/subscription"
)

func TestChatRefreshesOnceAfterUnauthorized(t *testing.T) {
	source := &fakeCredentialSource{
		credential: subscription.Credential{AccessToken: "access-old"},
		refreshed:  subscription.Credential{AccessToken: "access-new"},
	}
	requests := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.String() != DefaultAPIBase+"/responses" {
			t.Fatalf("URL = %q", r.URL.String())
		}
		want := "Bearer access-new"
		if requests == 1 {
			want = "Bearer access-old"
		}
		if got := r.Header.Get("Authorization"); got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		if r.Header.Get("X-API-Key") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Fatalf("unsafe caller headers were retained: %#v", r.Header)
		}
		if requests == 1 {
			writeAPIError(w, http.StatusUnauthorized)
			return
		}
		writeResponse(w, "ok")
	})
	provider, err := New(Config{
		CredentialSource: source,
		DefaultModel:     "grok-4.5",
		HTTPClient:       testHTTPClient(handler),
		Headers: map[string]string{
			"Authorization":       "caller-controlled",
			"Proxy-Authorization": "caller-controlled",
			"X-API-Key":           "caller-controlled",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Chat(context.Background(), &chat.Request{Messages: []chat.Message{chat.User("hello")}})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if result.Text != "ok" || result.Usage.Cost != nil {
		t.Fatalf("result = %+v", result)
	}
	if requests != 2 || source.credentialCalls != 1 || source.refreshCalls != 1 || source.rejected != "access-old" {
		t.Fatalf("calls: requests=%d credential=%d refresh=%d rejected=%q", requests, source.credentialCalls, source.refreshCalls, source.rejected)
	}
}

func TestChatForwardsRawEasyMessageInput(t *testing.T) {
	source := &fakeCredentialSource{credential: subscription.Credential{AccessToken: "access"}}
	requests := 0
	provider, err := New(Config{
		CredentialSource: source,
		DefaultModel:     "grok-4.5",
		HTTPClient: testHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			input, ok := payload["input"].([]any)
			if !ok || len(input) != 1 {
				t.Fatalf("input = %#v", payload["input"])
			}
			message, ok := input[0].(map[string]any)
			if !ok || message["role"] != "user" || message["content"] != "hello" {
				t.Fatalf("message = %#v", input[0])
			}
			writeResponse(w, "ok")
		})),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Chat(context.Background(), &chat.Request{
		Model: "grok-4.5",
		Options: chat.Options{OpenAI: structs.JSONMap{
			"input": []map[string]any{{"role": "user", "content": "hello"}},
		}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if requests != 1 || result.Text != "ok" {
		t.Fatalf("requests = %d result = %#v", requests, result)
	}
}

func TestChatReusesUpstreamClientForUnchangedCredential(t *testing.T) {
	source := &fakeCredentialSource{credential: subscription.Credential{AccessToken: "access"}}
	requests := 0
	provider, err := New(Config{
		CredentialSource: source,
		DefaultModel:     "grok-4.5",
		HTTPClient: testHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests++
			writeResponse(w, "ok")
		})),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := &chat.Request{Messages: []chat.Message{chat.User("hello")}}
	if _, err := provider.Chat(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	first := provider.upstream
	if _, err := provider.Chat(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if first == nil || provider.upstream != first {
		t.Fatal("upstream client was rebuilt for an unchanged credential")
	}
	if requests != 2 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestChatDoesNotRefreshNonUnauthorizedErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   error
	}{
		{name: "forbidden", status: http.StatusForbidden, want: ErrEntitlement},
		{name: "not found", status: http.StatusNotFound, want: ErrModelUnavailable},
		{name: "rate limited", status: http.StatusTooManyRequests, want: ErrRateLimited},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &fakeCredentialSource{credential: subscription.Credential{AccessToken: "access"}}
			provider, err := New(Config{
				CredentialSource: source,
				DefaultModel:     "grok-4.5",
				HTTPClient: testHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if tt.status == http.StatusTooManyRequests {
						w.Header().Set("Retry-After", "2")
					}
					writeAPIError(w, tt.status)
				})),
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.Chat(context.Background(), &chat.Request{Messages: []chat.Message{chat.User("hello")}})
			if !errors.Is(err, tt.want) {
				t.Fatalf("Chat() error = %v, want %v", err, tt.want)
			}
			if source.refreshCalls != 0 {
				t.Fatalf("refresh calls = %d", source.refreshCalls)
			}
			if tt.status == http.StatusTooManyRequests && !strings.Contains(err.Error(), "2s") {
				t.Fatalf("rate-limit error = %v", err)
			}
		})
	}
}

func TestChatDoesNotRetryWhenRefreshReturnsRejectedToken(t *testing.T) {
	source := &fakeCredentialSource{
		credential: subscription.Credential{AccessToken: "same"},
		refreshed:  subscription.Credential{AccessToken: " same "},
	}
	requests := 0
	provider, err := New(Config{
		CredentialSource: source,
		DefaultModel:     "grok-4.5",
		HTTPClient: testHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests++
			writeAPIError(w, http.StatusUnauthorized)
		})),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Chat(context.Background(), &chat.Request{Messages: []chat.Message{chat.User("hello")}})
	if !errors.Is(err, ErrUnauthorized) || requests != 1 {
		t.Fatalf("Chat() error=%v requests=%d", err, requests)
	}
}

func TestChatStopsAfterSecondUnauthorized(t *testing.T) {
	source := &fakeCredentialSource{
		credential: subscription.Credential{AccessToken: "old"},
		refreshed:  subscription.Credential{AccessToken: "new"},
	}
	requests := 0
	provider, err := New(Config{
		CredentialSource: source,
		DefaultModel:     "grok-4.5",
		HTTPClient: testHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests++
			writeAPIError(w, http.StatusUnauthorized)
		})),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Chat(context.Background(), &chat.Request{Messages: []chat.Message{chat.User("hello")}})
	if !errors.Is(err, ErrUnauthorized) || requests != 2 || source.refreshCalls != 1 {
		t.Fatalf("Chat() error=%v requests=%d refresh=%d", err, requests, source.refreshCalls)
	}
}

type fakeCredentialSource struct {
	credential      subscription.Credential
	refreshed       subscription.Credential
	credentialCalls int
	refreshCalls    int
	rejected        string
}

func (s *fakeCredentialSource) Credential(context.Context) (subscription.Credential, error) {
	s.credentialCalls++
	return s.credential, nil
}

func (s *fakeCredentialSource) RefreshRejected(_ context.Context, rejected string) (subscription.Credential, error) {
	s.refreshCalls++
	s.rejected = rejected
	return s.refreshed, nil
}

func testHTTPClient(handler http.Handler) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req.Clone(req.Context()))
		return recorder.Result(), nil
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func writeAPIError(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("x-should-retry", "false")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
		"message": "rejected", "type": "request_error", "code": "rejected",
	}})
}

func writeResponse(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "resp_test", "object": "response", "model": "grok-4.5", "status": "completed",
		"parallel_tool_calls": true,
		"output": []any{map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
		}},
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
	})
}
