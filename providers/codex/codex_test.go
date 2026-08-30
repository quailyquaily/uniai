package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/openai/openai-go/v3/responses"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/subscription"
)

func TestChatRefreshesOnceAfterUnauthorized(t *testing.T) {
	source := &fakeCredentialSource{
		credential: subscription.Credential{AccessToken: "access-old", AccountID: "account-old"},
		refreshed:  subscription.Credential{AccessToken: "access-new", AccountID: "account-new"},
	}
	requests := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.String() != DefaultAPIBase+"/responses" {
			t.Fatalf("URL = %q", r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-"+map[bool]string{true: "old", false: "new"}[requests == 1] {
			t.Fatalf("Authorization = %q", got)
		}
		wantAccount := "account-new"
		if requests == 1 {
			wantAccount = "account-old"
		}
		if got := r.Header.Get("ChatGPT-Account-ID"); got != wantAccount {
			t.Fatalf("ChatGPT-Account-ID = %q, want %q", got, wantAccount)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["instructions"] != "Follow the project rules." || payload["store"] != false {
			t.Fatalf("payload instructions/store = %#v", payload)
		}
		if payload["stream"] != true {
			t.Fatalf("codex subscription request must stream: %#v", payload["stream"])
		}
		data, _ := json.Marshal(payload["input"])
		if strings.Contains(string(data), `"role":"system"`) {
			t.Fatalf("system message remained in input: %s", data)
		}
		if requests == 1 {
			writeAPIError(w, http.StatusUnauthorized)
			return
		}
		writeResponse(w, "ok")
	})

	provider, err := New(Config{
		CredentialSource: source,
		DefaultModel:     "gpt-5.4",
		HTTPClient:       testHTTPClient(handler),
		Headers: map[string]string{
			"Authorization":      "Bearer caller-controlled",
			"ChatGPT-Account-ID": "caller-controlled",
			"X-Test":             "preserved",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Chat(context.Background(), &chat.Request{Messages: []chat.Message{
		chat.System("Follow the project rules."),
		chat.User("hello"),
	}})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if result.Text != "ok" || result.Usage.Cost != nil {
		t.Fatalf("result = %+v", result)
	}
	if source.credentialCalls != 1 || source.refreshCalls != 1 || source.rejected != "access-old" || requests != 2 {
		t.Fatalf("calls: credential=%d refresh=%d rejected=%q requests=%d", source.credentialCalls, source.refreshCalls, source.rejected, requests)
	}
}

func TestChatReusesUpstreamClientForUnchangedCredential(t *testing.T) {
	source := &fakeCredentialSource{credential: subscription.Credential{
		AccessToken: "access", AccountID: "account",
	}}
	requests := 0
	provider, err := New(Config{
		CredentialSource: source,
		DefaultModel:     "gpt-5.4",
		HTTPClient: testHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests++
			writeResponse(w, "ok")
		})),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := &chat.Request{Messages: []chat.Message{chat.System("rules"), chat.User("hello")}}
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

func TestChatPassesImageGenerationToolAndPreservesResult(t *testing.T) {
	source := &fakeCredentialSource{credential: subscription.Credential{
		AccessToken: "access", AccountID: "account",
	}}
	provider, err := New(Config{
		CredentialSource: source,
		DefaultModel:     "gpt-5.6-sol",
		HTTPClient: testHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["model"] != "gpt-5.6-sol" || payload["input"] != "Draw a lighthouse." {
				t.Fatalf("payload model/input = %#v", payload)
			}
			tools, ok := payload["tools"].([]any)
			if !ok || len(tools) != 1 {
				t.Fatalf("tools = %#v", payload["tools"])
			}
			tool, ok := tools[0].(map[string]any)
			if !ok || tool["type"] != "image_generation" || tool["model"] != "gpt-image-2" {
				t.Fatalf("image tool = %#v", tools[0])
			}

			w.Header().Set("Content-Type", "text/event-stream")
			response := map[string]any{
				"id": "resp_image", "object": "response", "model": "gpt-5.6-sol", "status": "completed",
				"parallel_tool_calls": true,
				"output": []any{map[string]any{
					"id": "ig_1", "type": "image_generation_call", "status": "completed", "result": "QUJD",
				}},
				"usage": map[string]any{"input_tokens": 4, "output_tokens": 8, "total_tokens": 12},
			}
			data, _ := json.Marshal(map[string]any{
				"type": "response.completed", "sequence_number": 1, "response": response,
			})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		})),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Chat(context.Background(), &chat.Request{
		Model: "gpt-5.6-sol",
		Options: chat.Options{OpenAI: structs.JSONMap{
			"instructions": "You are a helpful assistant.",
			"input":        "Draw a lighthouse.",
			"tools": []any{map[string]any{
				"type": "image_generation", "action": "generate", "model": "gpt-image-2",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	raw, ok := result.Raw.(*responses.Response)
	if !ok || len(raw.Output) != 1 {
		t.Fatalf("raw response = %#v", result.Raw)
	}
	call, ok := raw.Output[0].AsAny().(responses.ResponseOutputItemImageGenerationCall)
	if !ok || call.Result != "QUJD" {
		t.Fatalf("image generation call = %#v", raw.Output[0].AsAny())
	}
}

func TestChatRebuildsUpstreamClientWhenAccountChanges(t *testing.T) {
	source := &fakeCredentialSource{credential: subscription.Credential{
		AccessToken: "access", AccountID: "account-one",
	}}
	var accounts []string
	provider, err := New(Config{
		CredentialSource: source,
		DefaultModel:     "gpt-5.4",
		HTTPClient: testHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			accounts = append(accounts, r.Header.Get("ChatGPT-Account-ID"))
			writeResponse(w, "ok")
		})),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := &chat.Request{Messages: []chat.Message{chat.System("rules"), chat.User("hello")}}
	if _, err := provider.Chat(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	first := provider.upstream
	source.credential.AccountID = "account-two"
	if _, err := provider.Chat(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if provider.upstream == first {
		t.Fatal("upstream client was reused after the account changed")
	}
	if got := strings.Join(accounts, ","); got != "account-one,account-two" {
		t.Fatalf("accounts = %s", got)
	}
}

func TestChatDoesNotRetryWhenRefreshReturnsRejectedToken(t *testing.T) {
	source := &fakeCredentialSource{
		credential: subscription.Credential{AccessToken: "same", AccountID: "account"},
		refreshed:  subscription.Credential{AccessToken: " same ", AccountID: "account"},
	}
	requests := 0
	provider, err := New(Config{
		CredentialSource: source,
		DefaultModel:     "gpt-5.4",
		HTTPClient: testHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests++
			writeAPIError(w, http.StatusUnauthorized)
		})),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Chat(context.Background(), &chat.Request{Messages: []chat.Message{
		chat.System("rules"), chat.User("hello"),
	}})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Chat() error = %v, want ErrUnauthorized", err)
	}
	if requests != 1 || source.refreshCalls != 1 {
		t.Fatalf("requests=%d refresh=%d", requests, source.refreshCalls)
	}
}

func TestChatStopsAfterSecondUnauthorized(t *testing.T) {
	source := &fakeCredentialSource{
		credential: subscription.Credential{AccessToken: "old", AccountID: "account"},
		refreshed:  subscription.Credential{AccessToken: "new", AccountID: "account"},
	}
	requests := 0
	provider, err := New(Config{
		CredentialSource: source,
		DefaultModel:     "gpt-5.4",
		HTTPClient: testHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests++
			writeAPIError(w, http.StatusUnauthorized)
		})),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Chat(context.Background(), &chat.Request{Messages: []chat.Message{
		chat.System("rules"), chat.User("hello"),
	}})
	if !errors.Is(err, ErrUnauthorized) || requests != 2 || source.refreshCalls != 1 {
		t.Fatalf("Chat() error=%v requests=%d refresh=%d", err, requests, source.refreshCalls)
	}
}

func TestChatRejectsEmptyCredentialBeforeRequest(t *testing.T) {
	provider, err := New(Config{
		CredentialSource: &fakeCredentialSource{},
		DefaultModel:     "gpt-5.4",
		HTTPClient: testHTTPClient(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("request must not be sent")
		})),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Chat(context.Background(), &chat.Request{Messages: []chat.Message{
		chat.System("rules"), chat.User("hello"),
	}})
	if err == nil || !strings.Contains(err.Error(), "access token") {
		t.Fatalf("Chat() error = %v", err)
	}
}

func TestChatHTTPErrorDoesNotExposeCredentialOrResponseBody(t *testing.T) {
	source := &fakeCredentialSource{credential: subscription.Credential{
		AccessToken: "access-secret", AccountID: "account",
	}}
	provider, err := New(Config{
		CredentialSource: source,
		DefaultModel:     "gpt-5.4",
		HTTPClient: testHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
				"message": "access-secret was rejected with private upstream detail",
			}})
		})),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Chat(context.Background(), &chat.Request{Messages: []chat.Message{
		chat.System("rules"), chat.User("hello"),
	}})
	if err == nil {
		t.Fatal("Chat() expected error")
	}
	if strings.Contains(err.Error(), "access-secret") || strings.Contains(err.Error(), "private upstream detail") {
		t.Fatalf("Chat() leaked upstream error detail: %v", err)
	}
}

type fakeCredentialSource struct {
	credential      subscription.Credential
	refreshed       subscription.Credential
	credentialErr   error
	refreshErr      error
	credentialCalls int
	refreshCalls    int
	rejected        string
}

func (s *fakeCredentialSource) Credential(context.Context) (subscription.Credential, error) {
	s.credentialCalls++
	return s.credential, s.credentialErr
}

func (s *fakeCredentialSource) RefreshRejected(_ context.Context, rejected string) (subscription.Credential, error) {
	s.refreshCalls++
	s.rejected = rejected
	return s.refreshed, s.refreshErr
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
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
		"message": "rejected", "type": "authentication_error", "code": "unauthorized",
	}})
}

func writeResponse(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	response := map[string]any{
		"id": "resp_test", "object": "response", "model": "gpt-5.4", "status": "completed",
		"parallel_tool_calls": true,
		"output": []any{map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
		}},
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
	}
	data, _ := json.Marshal(map[string]any{
		"type": "response.completed", "sequence_number": 1, "response": response,
	})
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
}
