package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quailyquaily/uniai"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/subscription"
	claudeauth "github.com/quailyquaily/uniai/subscription/claude"
)

func TestClaudeCredentialRefreshIsSerializedAndPersisted(t *testing.T) {
	now := time.Now().UTC()
	path := t.TempDir() + "/claude.json"
	old := claudeauth.Token{AccessToken: "old", RefreshToken: "refresh", AccountID: "account", Scope: "user:inference", CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}
	if err := writeTokenFile(path, old); err != nil {
		t.Fatal(err)
	}
	s := newClaudeCredentialSource(path, claudeauth.OAuthConfig{})
	s.now = func() time.Time { return now }
	var refreshes atomic.Int32
	s.refresh = func(context.Context, claudeauth.OAuthConfig, string) (claudeauth.Token, error) {
		refreshes.Add(1)
		return claudeauth.Token{AccessToken: "new", RefreshToken: "rotated", ExpiresAt: now.Add(time.Hour)}, nil
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := s.RefreshRejected(context.Background(), "old")
			if err != nil || c.AccessToken != "new" || c.AccountID != "account" {
				t.Errorf("credential=%+v err=%v", c, err)
			}
		}()
	}
	wg.Wait()
	if refreshes.Load() != 1 {
		t.Fatalf("refreshes=%d", refreshes.Load())
	}
	saved, err := readTokenFile[claudeauth.Token](path)
	if err != nil || saved.RefreshToken != "rotated" || saved.Scope != old.Scope || !saved.CreatedAt.Equal(old.CreatedAt) {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
}

func TestClaudeCredentialSourceRetainsRotationWhenSaveFails(t *testing.T) {
	now := time.Now().UTC()
	path := t.TempDir() + "/claude.json"
	if err := writeTokenFile(path, claudeauth.Token{AccessToken: "old", RefreshToken: "old-refresh", AccountID: "account", ExpiresAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	s := newClaudeCredentialSource(path, claudeauth.OAuthConfig{})
	calls := 0
	s.refresh = func(context.Context, claudeauth.OAuthConfig, string) (claudeauth.Token, error) {
		calls++
		s.tokenPath = t.TempDir() // Renaming a file onto a directory must fail.
		return claudeauth.Token{AccessToken: "new", RefreshToken: "new-refresh", ExpiresAt: now.Add(time.Hour)}, nil
	}
	if _, err := s.Credential(context.Background()); err == nil {
		t.Fatal("expected persistence failure")
	}
	s.tokenPath = path
	c, err := s.Credential(context.Background())
	if err != nil || c.AccessToken != "new" || calls != 1 {
		t.Fatalf("credential=%+v calls=%d err=%v", c, calls, err)
	}
	saved, err := readTokenFile[claudeauth.Token](path)
	if err != nil || saved.RefreshToken != "new-refresh" {
		t.Fatalf("rotation not recovered: %v", err)
	}
}

func TestClaudeCredentialSourceKeepsTokenOnTransientFailure(t *testing.T) {
	path := t.TempDir() + "/claude.json"
	old := claudeauth.Token{AccessToken: "old", RefreshToken: "refresh", AccountID: "account"}
	writeTokenFile(path, old)
	s := newClaudeCredentialSource(path, claudeauth.OAuthConfig{})
	s.refresh = func(context.Context, claudeauth.OAuthConfig, string) (claudeauth.Token, error) {
		return claudeauth.Token{}, errors.New("temporary failure")
	}
	if _, err := s.Credential(context.Background()); err == nil {
		t.Fatal("expected refresh failure")
	}
	saved, err := readTokenFile[claudeauth.Token](path)
	if err != nil || saved.RefreshToken != "refresh" {
		t.Fatal("lost stored token")
	}
}

type claudeTransport func(*http.Request) (*http.Response, error)

func (f claudeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type loginReader func([]byte) (int, error)

func (f loginReader) Read(p []byte) (int, error) { return f(p) }

func TestClaudeLoginStatusAndLogout(t *testing.T) {
	var output bytes.Buffer
	path := t.TempDir() + "/claude.json"
	cfg := claudeauth.OAuthConfig{HTTPClient: &http.Client{Transport: claudeTransport(func(r *http.Request) (*http.Response, error) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["code"] != "approved" {
			t.Fatal("wrong authorization code")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"access_token":"secret-access","refresh_token":"secret-refresh","account":{"uuid":"account"},"expires_in":3600}`))}, nil
	})}}
	input := loginReader(func(p []byte) (int, error) {
		for _, word := range strings.Fields(output.String()) {
			if strings.HasPrefix(word, "https://") {
				u, err := url.Parse(word)
				if err != nil {
					return 0, err
				}
				return copy(p, "approved#"+u.Query().Get("state")+"\n"), io.EOF
			}
		}
		return 0, fmt.Errorf("authorization URL not printed")
	})
	if err := loginClaude(context.Background(), path, cfg, input, &output); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"status", "--backend", "claude", "--token-file", path}, &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "backend=claude logged_in=true") || strings.Contains(output.String(), "secret-") {
		t.Fatalf("unsafe status output: %s", output.String())
	}
	if err := run(context.Background(), []string{"logout", "--backend", "claude", "--token-file", path}, &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("logout: %v", err)
	}
}

func TestClaudeLoginCanBeCanceledWhileWaitingForInput(t *testing.T) {
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := loginClaude(ctx, t.TempDir()+"/claude.json", claudeauth.OAuthConfig{}, input, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
}

func TestClaudeProxyRoutesChatAndRejectsResponses(t *testing.T) {
	called := 0
	runner := &fakeChatRunner{run: func(_ context.Context, opts ...chat.Option) (*chat.Result, error) {
		called++
		req, err := chat.BuildRequest(opts...)
		if err != nil {
			return nil, err
		}
		if len(req.Messages) != 2 || req.Messages[0].Role != chat.RoleSystem || req.Messages[1].Role != chat.RoleUser {
			t.Fatalf("messages=%+v", req.Messages)
		}
		return &chat.Result{Text: "ok"}, nil
	}}
	h := newRoutedAPIHandler(map[string]chatRunner{backendClaude: runner, backendCodex: nil, backendXAI: nil}, "")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"claude-sonnet-4-6","messages":[{"role":"system","content":"rules"},{"role":"user","content":"hi"}]}`)))
	if r.Code != 200 || called != 1 {
		t.Fatalf("chat=%d %s", r.Code, r.Body.String())
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"claude-sonnet-4-6","input":"hi"}`)))
	if r.Code != 400 || called != 1 || !strings.Contains(r.Body.String(), "/v1/chat/completions") {
		t.Fatalf("responses=%d %s", r.Code, r.Body.String())
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/healthz", nil))
	if !strings.Contains(r.Body.String(), `"backend":"claude"`) {
		t.Fatalf("health=%s", r.Body.String())
	}
}

func TestClaudeAndCodexServeConfiguration(t *testing.T) {
	dir := t.TempDir()
	err := run(context.Background(), []string{"serve", "--codex-token-file", dir + "/codex.json", "--claude-token-file", dir + "/claude.json"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "load or refresh codex credentials") {
		t.Fatalf("serve error=%v", err)
	}
	err = run(context.Background(), []string{"serve", "--codex-token-file", dir + "/same.json", "--claude-token-file", dir + "/same.json"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "distinct token files") {
		t.Fatalf("duplicate files: %v", err)
	}
}

var _ subscription.CredentialSource = (*claudeCredentialSource)(nil)

func TestClaudeProxyEndToEndOffline(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			path := t.TempDir() + "/claude.json"
			if err := writeTokenFile(path, claudeauth.Token{AccessToken: "access", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			client := uniai.New(uniai.Config{
				Provider: "claude_oauth", AnthropicModel: "claude-sonnet-4-6", ClaudeSubscription: newClaudeCredentialSource(path, claudeauth.OAuthConfig{}),
				SubscriptionHTTPClient: &http.Client{Transport: claudeTransport(func(r *http.Request) (*http.Response, error) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if r.URL.Host != "api.anthropic.com" || r.Header.Get("Authorization") != "Bearer access" || body["model"] != "claude-sonnet-4-6" || body["max_tokens"] != float64(128) {
						t.Fatalf("upstream request=%s body=%+v", r.URL, body)
					}
					tools := body["tools"].([]any)
					if len(tools) != 1 || tools[0].(map[string]any)["name"] != "lookup" {
						t.Fatalf("tools=%+v", tools)
					}
					choice, ok := body["tool_choice"].(map[string]any)
					if !ok || choice["name"] != "lookup" || choice["disable_parallel_tool_use"] != true {
						t.Errorf("tool_choice=%+v", choice)
					}
					payload := `{"id":"msg_1","model":"claude-sonnet-4-6","content":[{"type":"tool_use","id":"call_1","name":"lookup","input":{"n":9007199254740993}}],"usage":{"input_tokens":3,"output_tokens":4}}`
					if stream {
						if body["stream"] != true {
							t.Fatal("stream flag missing")
						}
						payload = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude-sonnet-4-6\",\"usage\":{\"input_tokens\":3}}}\n\n" +
							"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_1\",\"name\":\"lookup\"}}\n\n" +
							"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"n\\\":9007199254740993}\"}}\n\n" +
							"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
							"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":4}}\n\n" +
							"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload))}, nil
				})},
			})
			h := newAPIHandler(client, backendClaude, "claude-sonnet-4-6")
			r := httptest.NewRecorder()
			payload := fmt.Sprintf(`{"stream":%t,"parallel_tool_calls":false,"tool_choice":{"type":"function","function":{"name":"lookup"}},"max_completion_tokens":128,"messages":[{"role":"system","content":"rules"},{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"n":{"type":"integer"}}}}}]}`, stream)
			h.ServeHTTP(r, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(payload)))
			body := r.Body.String()
			if r.Code != 200 || !strings.Contains(body, "lookup") || !strings.Contains(body, "9007199254740993") || !strings.Contains(body, "tool_calls") {
				t.Fatalf("proxy=%d %s", r.Code, body)
			}
			if stream && (!strings.Contains(body, "[DONE]") || strings.Contains(body, `"error"`)) {
				t.Fatalf("bad stream=%s", body)
			}
		})
	}
}

func TestClaudeServeFlagValidation(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"serve", "--backend", "claude", "--model", "claude-sonnet-4-6"}, "--token-file is required"},
		{[]string{"serve", "--backend", "claude", "--token-file", "claude.json"}, "--model is required"},
		{[]string{"serve", "--backend", "claude", "--claude-token-file", "claude.json"}, "cannot be combined"},
		{[]string{"serve", "--claude-client-id", "client"}, "--claude-token-file is required"},
	} {
		err := run(context.Background(), tc.args, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("args=%v err=%v want=%s", tc.args, err, tc.want)
		}
	}
}

func TestClaudeProxyFinishReasonsOffline(t *testing.T) {
	path := t.TempDir() + "/claude.json"
	if err := writeTokenFile(path, claudeauth.Token{AccessToken: "access", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		reason, want string
		tool         bool
	}{
		{reason: "end_turn", want: "stop"},
		{reason: "max_tokens", want: "length"},
		{reason: "max_tokens", want: "length", tool: true},
		{reason: "model_context_window_exceeded", want: "length"},
		{reason: "tool_use", want: "tool_calls", tool: true},
		{reason: "refusal", want: "content_filter"},
		{reason: "pause_turn"},
		{reason: "unknown_reason"},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/tool=%t/stream=%t", tt.reason, tt.tool, stream), func(t *testing.T) {
				calls := 0
				client := uniai.New(uniai.Config{
					Provider: "claude_oauth", AnthropicModel: "claude-sonnet-4-6", ClaudeSubscription: newClaudeCredentialSource(path, claudeauth.OAuthConfig{}),
					SubscriptionHTTPClient: &http.Client{Transport: claudeTransport(func(r *http.Request) (*http.Response, error) {
						calls++
						var body struct {
							MaxTokens int  `json:"max_tokens"`
							Stream    bool `json:"stream"`
						}
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Fatal(err)
						}
						if body.MaxTokens != 1 || body.Stream != stream {
							t.Fatalf("upstream body=%+v", body)
						}
						content := `{"type":"text","text":"{"}`
						if tt.tool {
							content = `{"type":"tool_use","id":"call_1","name":"lookup","input":{}}`
						}
						payload := fmt.Sprintf(`{"id":"msg_1","model":"claude-sonnet-4-6","content":[%s],"stop_reason":%q,"usage":{"input_tokens":3,"output_tokens":1}}`, content, tt.reason)
						if stream {
							blocks := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"{\"}}\n\n"
							if tt.tool {
								args := `{}`
								if tt.reason == "max_tokens" {
									args = `{"n":`
								}
								blocks = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_1\",\"name\":\"lookup\"}}\n\n" +
									fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%q}}\n\n", args)
							}
							payload = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude-sonnet-4-6\",\"usage\":{\"input_tokens\":3}}}\n\n" + blocks +
								"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
								fmt.Sprintf("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":%q},\"usage\":{\"output_tokens\":1}}\n\n", tt.reason) +
								"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
						}
						return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload))}, nil
					})},
				})
				recorder := httptest.NewRecorder()
				request := fmt.Sprintf(`{"messages":[{"role":"user","content":"hi"}],"max_completion_tokens":1,"stream":%t}`, stream)
				newAPIHandler(client, backendClaude, "claude-sonnet-4-6").ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(request)))
				if calls != 1 {
					t.Fatalf("unexpected retry: calls=%d", calls)
				}
				body := recorder.Body.String()
				if tt.want == "" {
					wantStatus := http.StatusBadGateway
					if stream {
						wantStatus = http.StatusOK
					}
					if recorder.Code != wantStatus || !strings.Contains(body, `"upstream_error"`) || strings.Contains(body, `"finish_reason":"`) {
						t.Fatalf("unsupported reason reported as success: status=%d body=%s", recorder.Code, body)
					}
					return
				}
				if recorder.Code != http.StatusOK || strings.Contains(body, `"error"`) || strings.Count(body, `"finish_reason":"`+tt.want+`"`) != 1 {
					t.Fatalf("status=%d want reason=%q body=%s", recorder.Code, tt.want, body)
				}
				if stream && !strings.Contains(body, "data: [DONE]") {
					t.Fatalf("missing stream terminator: %s", body)
				}
			})
		}
	}
}
