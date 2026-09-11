package uniai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/subscription"
)

func TestClientOpenAICodexUsesSubscriptionAndCalculatesCost(t *testing.T) {
	source := &rootCredentialSource{credential: subscription.Credential{AccessToken: "codex-token", AccountID: "account"}}
	httpClient := rootTestHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.String() != "https://chatgpt.com/backend-api/codex/responses" {
			t.Fatalf("URL = %q", r.URL.String())
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["stream"] != true {
			t.Fatalf("codex subscription request must stream: %#v", payload["stream"])
		}
		rootWriteResponseStream(w, "gpt-5.4")
	}))
	client := New(Config{
		Provider:               "openai_codex",
		OpenAIModel:            "gpt-5.4",
		Pricing:                rootSubscriptionPricing(),
		CodexSubscription:      source,
		SubscriptionHTTPClient: httpClient,
	})
	result, err := client.Chat(context.Background(), chat.WithMessages(
		chat.System("rules"), chat.User("hello"),
	))
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if result.Usage.Cost == nil {
		t.Fatal("expected subscription cost")
	}
	assertNearlyEqual(t, result.Usage.Cost.Input, 0.0002)
	assertNearlyEqual(t, result.Usage.Cost.Output, 0.0003)
	assertNearlyEqual(t, result.Usage.Cost.Total, 0.0005)
	if source.calls != 1 {
		t.Fatalf("credential calls = %d", source.calls)
	}
}

func TestClientXAIOAuthCalculatesStreamingCost(t *testing.T) {
	source := &rootCredentialSource{credential: subscription.Credential{AccessToken: "xai-token"}}
	httpClient := rootTestHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rootWriteResponseStream(w, "grok-4.5")
	}))
	var finalUsage *chat.Usage
	client := New(Config{
		Provider:               "xai_oauth",
		OpenAIModel:            "grok-4.5",
		Pricing:                rootSubscriptionPricing(),
		XAISubscription:        source,
		SubscriptionHTTPClient: httpClient,
	})
	result, err := client.Chat(context.Background(),
		chat.WithMessages(chat.User("hello")),
		chat.WithOnStream(func(event chat.StreamEvent) error {
			if event.Done {
				finalUsage = event.Usage
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if result.Usage.Cost == nil || finalUsage == nil || finalUsage.Cost == nil {
		t.Fatalf("result cost=%#v final usage=%#v", result.Usage.Cost, finalUsage)
	}
	assertNearlyEqual(t, result.Usage.Cost.Total, 0.0005)
	assertNearlyEqual(t, finalUsage.Cost.Total, 0.0005)
}

func rootSubscriptionPricing() *PricingCatalog {
	return &PricingCatalog{Chat: []ChatPricingRule{
		{Model: "gpt-5.4", InputUSDPerMillion: 2, OutputUSDPerMillion: 6},
		{Model: "grok-4.5", InputUSDPerMillion: 2, OutputUSDPerMillion: 6},
	}}
}

func TestClientClaudeOAuthUsesSubscriptionNotAPIKey(t *testing.T) {
	source := &rootCredentialSource{credential: subscription.Credential{AccessToken: "claude-token", AccountID: "account"}}
	client := New(Config{
		Provider: "claude_oauth", AnthropicModel: "claude-sonnet-4-6", AnthropicAPIKey: "wrong", AnthropicAPIBase: "https://other.example",
		ClaudeSubscription: source,
		SubscriptionHTTPClient: rootTestHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Host != "api.anthropic.com" || r.Header.Get("Authorization") != "Bearer claude-token" {
				t.Fatalf("request = %s", r.URL)
			}
			fmt.Fprint(w, `{"model":"claude-sonnet-4-6","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":2,"output_tokens":1}}`)
		})),
	})
	result, err := client.Chat(context.Background(), chat.WithMessages(chat.User("hello")))
	if err != nil || result.Text != "ok" {
		t.Fatalf("Chat = %+v, %v", result, err)
	}
	view := client.GetConfig()
	if view.Model != "claude-sonnet-4-6" || view.APIBase != DefaultAnthropicAPIBase {
		t.Fatalf("config = %+v", view)
	}
	client = New(Config{Provider: "claude_oauth", AnthropicAPIKey: "must-not-fall-back"})
	if _, err := client.Chat(context.Background(), chat.WithMessages(chat.User("hello"))); err == nil {
		t.Fatal("missing credential source accepted")
	}
}

type rootCredentialSource struct {
	credential subscription.Credential
	calls      int
}

func (s *rootCredentialSource) Credential(context.Context) (subscription.Credential, error) {
	s.calls++
	return s.credential, nil
}

func (s *rootCredentialSource) RefreshRejected(context.Context, string) (subscription.Credential, error) {
	return s.credential, nil
}

func rootTestHTTPClient(handler http.Handler) *http.Client {
	return &http.Client{Transport: rootRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req.Clone(req.Context()))
		return recorder.Result(), nil
	})}
}

type rootRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn rootRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func rootWriteResponse(w http.ResponseWriter, model string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rootResponse(model))
}

func rootWriteResponseStream(w http.ResponseWriter, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	data, _ := json.Marshal(map[string]any{
		"type": "response.completed", "sequence_number": 1, "response": rootResponse(model),
	})
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
}

func rootResponse(model string) map[string]any {
	return map[string]any{
		"id": "resp_root", "object": "response", "model": model, "status": "completed",
		"parallel_tool_calls": true,
		"output": []any{map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": "ok", "annotations": []any{}}},
		}},
		"usage": map[string]any{"input_tokens": 100, "output_tokens": 50, "total_tokens": 150},
	}
}
