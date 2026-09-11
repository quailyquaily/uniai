package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/subscription"
	"github.com/quailyquaily/uniai/subscription/claude/claudecode"
)

type subscriptionSource struct {
	refreshes int
	unchanged bool
}

func (s *subscriptionSource) Credential(context.Context) (subscription.Credential, error) {
	return subscription.Credential{AccessToken: "old-token", AccountID: "account"}, nil
}

func (s *subscriptionSource) RefreshRejected(_ context.Context, token string) (subscription.Credential, error) {
	s.refreshes++
	if token != "old-token" {
		return subscription.Credential{}, fmt.Errorf("wrong rejected credential")
	}
	if s.unchanged {
		return s.Credential(context.Background())
	}
	return subscription.Credential{AccessToken: "new-token", AccountID: "account"}, nil
}

type subscriptionTransport func(*http.Request) (*http.Response, error)

func (f subscriptionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func subscriptionResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestSubscriptionRetries401OncePreservingRequest(t *testing.T) {
	source := &subscriptionSource{}
	calls := 0
	var firstBody string
	p := New(Config{
		CredentialSource: source, APIKey: "must-not-use", APIBase: "https://other.example", DefaultModel: "claude-sonnet-4-6",
		Headers: map[string]string{"authorization": "wrong", "x-api-key": "wrong"},
		HTTPClient: &http.Client{Transport: subscriptionTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.String() != claudecode.MessagesURL || r.Header.Get("X-Api-Key") != "" || r.Header.Get("X-App") != "cli" {
				t.Fatalf("request = %s headers=%v", r.URL, r.Header)
			}
			data, _ := io.ReadAll(r.Body)
			if calls == 1 {
				firstBody = string(data)
				if r.Header.Get("Authorization") != "Bearer old-token" {
					t.Fatal("wrong initial token")
				}
				return subscriptionResponse(401, `{"error":"old-token"}`), nil
			}
			if string(data) != firstBody || r.Header.Get("Authorization") != "Bearer new-token" {
				t.Fatal("retry changed request or failed to refresh")
			}
			var body map[string]any
			json.Unmarshal(data, &body)
			if body["model"] != "claude-sonnet-4-6" || len(body["system"].([]any)) != 2 {
				t.Fatalf("body = %s", data)
			}
			return subscriptionResponse(200, `{"id":"msg_1","model":"claude-sonnet-4-6","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":2}}`), nil
		})},
	})
	req := &chat.Request{Messages: []chat.Message{chat.System("rules"), chat.User("hi")}}
	result, err := p.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "msg_1" || result.Text != "hello" || result.Usage.InputTokens != 5 || calls != 2 || source.refreshes != 1 || len(req.Messages) != 2 {
		t.Fatalf("result=%+v calls=%d refreshes=%d", result, calls, source.refreshes)
	}
}

func TestSubscriptionDoesNotRetryForbiddenRateLimitsOrSameToken(t *testing.T) {
	for _, status := range []int{401, 403, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			source := &subscriptionSource{unchanged: true}
			calls := 0
			p := New(Config{CredentialSource: source, DefaultModel: "claude-sonnet-4-6", HTTPClient: &http.Client{Transport: subscriptionTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return subscriptionResponse(status, `{"error":"old-token secret"}`), nil
			})}})
			_, err := p.Chat(context.Background(), &chat.Request{Messages: []chat.Message{chat.User("hi")}})
			if err == nil || strings.Contains(err.Error(), "old-token") || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			wantRefresh := 0
			if status == 401 {
				wantRefresh = 1
			}
			if source.refreshes != wantRefresh {
				t.Fatalf("refreshes=%d", source.refreshes)
			}
		})
	}
}

func TestSubscriptionStreamingToolsAndCancellation(t *testing.T) {
	stream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude-sonnet-4-6\",\"usage\":{\"input_tokens\":3}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool_1\",\"name\":\"my_tool\",\"input\":{}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"x\\\":1}\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":4}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	p := New(Config{CredentialSource: &subscriptionSource{}, DefaultModel: "claude-sonnet-4-6", HTTPClient: &http.Client{Transport: subscriptionTransport(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] != true {
			t.Fatal("missing stream flag")
		}
		return subscriptionResponse(200, stream), nil
	})}})
	done := false
	req := &chat.Request{Messages: []chat.Message{chat.User("hi")}, Options: chat.Options{OnStream: func(ev chat.StreamEvent) error { done = done || ev.Done; return nil }}}
	result, err := p.Chat(context.Background(), req)
	if err != nil || !done || len(result.ToolCalls) != 1 || result.ToolCalls[0].Function.Name != "my_tool" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	stop := errors.New("consumer stopped")
	req.Options.OnStream = func(chat.StreamEvent) error { return stop }
	if _, err := p.Chat(context.Background(), req); !errors.Is(err, stop) {
		t.Fatalf("callback error=%v", err)
	}
}

func TestSubscriptionBlocksRedirects(t *testing.T) {
	calls := 0
	p := New(Config{CredentialSource: &subscriptionSource{}, DefaultModel: "claude-sonnet-4-6", HTTPClient: &http.Client{Transport: subscriptionTransport(func(*http.Request) (*http.Response, error) {
		calls++
		resp := subscriptionResponse(307, "")
		resp.Header.Set("Location", "https://other.example/messages")
		return resp, nil
	})}})
	_, err := p.Chat(context.Background(), &chat.Request{Messages: []chat.Message{chat.User("hi")}})
	if err == nil || calls != 1 {
		t.Fatalf("redirect calls=%d err=%v", calls, err)
	}
}

func TestSubscriptionStreamErrorsAreNotSuccessfulCompletions(t *testing.T) {
	for _, stream := range []string{"", "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"old-token\"}}\n\n", "data: {bad}\n\n"} {
		p := New(Config{CredentialSource: &subscriptionSource{}, DefaultModel: "claude-sonnet-4-6", HTTPClient: &http.Client{Transport: subscriptionTransport(func(*http.Request) (*http.Response, error) { return subscriptionResponse(200, stream), nil })}})
		done := false
		_, err := p.Chat(context.Background(), &chat.Request{Messages: []chat.Message{chat.User("hi")}, Options: chat.Options{OnStream: func(ev chat.StreamEvent) error { done = done || ev.Done; return nil }}})
		if err == nil || done || strings.Contains(err.Error(), "old-token") {
			t.Fatalf("done=%t err=%v", done, err)
		}
	}
}

func TestSubscriptionPreservesToolArgumentNumbers(t *testing.T) {
	const arguments = `{"n":9007199254740993}`
	p := New(Config{CredentialSource: &subscriptionSource{}, DefaultModel: "claude-sonnet-4-6", HTTPClient: &http.Client{Transport: subscriptionTransport(func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(data), arguments) {
			t.Errorf("tool replay lost numeric precision: %s", data)
		}
		return subscriptionResponse(200, `{"model":"claude-sonnet-4-6","content":[{"type":"tool_use","id":"next","name":"my_tool","input":`+arguments+`}],"usage":{}}`), nil
	})}})
	result, err := p.Chat(context.Background(), &chat.Request{Messages: []chat.Message{
		chat.User("hi"),
		{Role: chat.RoleAssistant, ToolCalls: []chat.ToolCall{{ID: "prior", Function: chat.ToolCallFunction{Name: "my_tool", Arguments: arguments}}}},
		{Role: chat.RoleTool, ToolCallID: "prior", Content: "ok"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Function.Arguments != arguments {
		t.Fatalf("tool response=%+v", result.ToolCalls)
	}
}

type afterCompletionReader struct{}

func (afterCompletionReader) Read([]byte) (int, error) {
	return 0, errors.New("read past message_stop")
}

func TestSubscriptionStreamStopsAtMessageStop(t *testing.T) {
	p := New(Config{CredentialSource: &subscriptionSource{}})
	_, err := p.chatStream(io.MultiReader(strings.NewReader("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"), afterCompletionReader{}), false, func(chat.StreamEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
}
