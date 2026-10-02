package uniai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/httputil"
)

func TestCountTokensMatchesChatInputs(t *testing.T) {
	for _, tt := range []struct {
		provider, model, countPath, body, chatBody string
		fields                                     []string
	}{
		{"anthropic", "claude-sonnet-5-5", "/v1/messages/count_tokens", `{"input_tokens":123}`, `{"model":"claude-sonnet-5-5","stop_reason":"end_turn","content":[],"usage":{}}`, []string{"model", "messages", "system", "tools", "tool_choice", "thinking", "output_config"}},
		{"gemini", "gemini-3.8-flash", "/v1beta/models/gemini-3.8-flash:countTokens", `{"totalTokens":123}`, `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`, []string{"contents", "systemInstruction", "tools", "toolConfig", "generationConfig"}},
		{"openai_resp", "gpt-6-sol", "/v1/responses/input_tokens", `{"input_tokens":123,"object":"response.input_tokens"}`, `{"id":"resp_test","status":"completed","model":"gpt-6-sol","output":[]}`, []string{"model", "input", "tools", "tool_choice", "reasoning", "text"}},
	} {
		t.Run(tt.provider, func(t *testing.T) {
			var payloads []map[string]any
			transport := rootRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				payloads = append(payloads, payload)
				if r.Header.Get("X-Chat-Test") != "same" {
					t.Error("missing configured headers")
				}
				body := tt.chatBody
				if len(payloads) > 1 {
					if r.Method != "POST" || r.URL.Path != tt.countPath {
						t.Errorf("unexpected counting endpoint: %s %s", r.Method, r.URL)
					}
					switch tt.provider {
					case "anthropic":
						if r.Header.Get("x-api-key") != "key" || r.Header.Get("anthropic-version") != "2023-06-01" {
							t.Error("incorrect Anthropic auth")
						}
					case "gemini":
						if r.Header.Get("x-goog-api-key") != "key" || r.URL.Query().Has("key") {
							t.Error("incorrect Gemini auth")
						}
					case "openai_resp":
						if r.Header.Get("Authorization") != "Bearer key" {
							t.Error("incorrect OpenAI auth")
						}
					}

					body = tt.body
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			original, originalTransport := httputil.DefaultClient, http.DefaultTransport
			httputil.DefaultClient = &http.Client{Transport: transport}
			http.DefaultTransport = transport
			t.Cleanup(func() { httputil.DefaultClient = original; http.DefaultTransport = originalTransport })
			c := New(Config{Provider: tt.provider, AnthropicAPIKey: "key", GeminiAPIKey: "key", OpenAIAPIKey: "key", AnthropicModel: tt.model, GeminiModel: tt.model, OpenAIModel: tt.model, ChatHeaders: map[string]string{"X-Chat-Test": "same"}})
			opts := []chat.Option{chat.WithMessages(chat.System("follow the instructions"), chat.UserParts(chat.TextPart("describe"), chat.ImageBase64Part("image/png", "QUJD"))), chat.WithTools([]chat.Tool{chat.FunctionTool("lookup", "look it up", []byte(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`))}), chat.WithToolChoice(chat.ToolChoiceAuto()), chat.WithReasoningEffort(chat.ReasoningEffortHigh), chat.WithMaxTokens(4096)}
			history := chat.AssistantToolCalls(chat.ToolCall{ID: "call_1", Type: "function", ThoughtSignature: "signed", Function: chat.ToolCallFunction{Name: "lookup", Arguments: `{"q":"hello"}`}})
			if tt.provider == "anthropic" {
				history.AnthropicContent = json.RawMessage(`[{"type":"thinking","thinking":"inspect","signature":"signed"},{"type":"tool_use","id":"call_1","name":"lookup","input":{"q":"hello"}}]`)
				opts = append(opts, chat.WithMessages(chat.SystemParts(chat.WithPartCacheControl(chat.TextPart("cached rules"), chat.CacheTTL5m()))))
			}
			opts = append(opts, chat.WithMessages(history, chat.ToolResult("call_1", `{"answer":"found"}`)))

			if _, err := c.Chat(context.Background(), opts...); err != nil {
				t.Fatal(err)
			}
			opts = append(opts, chat.WithOnStream(func(chat.StreamEvent) error { t.Fatal("counting invoked stream callback"); return nil }))
			got, err := c.CountTokens(context.Background(), opts...)
			if err != nil {
				t.Fatal(err)
			}
			if got.InputTokens != 123 || got.Model != tt.model {
				t.Fatalf("unexpected count: %+v", got)
			}
			if len(payloads) != 2 {
				t.Fatalf("unexpected number of requests: %d", len(payloads))
			}
			count := payloads[1]
			if tt.provider == "gemini" {
				count = count["generateContentRequest"].(map[string]any)
				if count["model"] != "models/"+tt.model {
					t.Errorf("missing Gemini model: %#v", count)
				}
			}
			for _, key := range tt.fields {
				if !reflect.DeepEqual(payloads[0][key], count[key]) {
					t.Errorf("%s differs: chat=%#v count=%#v", key, payloads[0][key], count[key])
				}
			}
			for _, key := range []string{"stream", "max_tokens", "max_output_tokens", "store", "metadata", "temperature", "top_p"} {
				if _, ok := count[key]; ok {
					t.Errorf("unexpected generation field %s", key)
				}
			}
		})
	}
}

func TestCountTokensPartialRequests(t *testing.T) {
	for _, provider := range []string{"anthropic", "gemini", "openai_resp"} {
		for _, part := range []string{"empty", "tools", "system", "history prefix"} {
			t.Run(provider+"/"+part, func(t *testing.T) {
				transport := rootRoundTripFunc(func(r *http.Request) (*http.Response, error) {
					data, _ := io.ReadAll(r.Body)
					// Partial inputs must not be padded with synthetic user messages.
					var obj map[string]any
					if err := json.Unmarshal(data, &obj); err != nil {
						t.Fatal(err)
					}
					if part == "empty" || part == "tools" || part == "system" {
						key := "messages"
						if provider == "gemini" {
							obj = obj["generateContentRequest"].(map[string]any)
							key = "contents"
						}
						if provider == "openai_resp" {
							key = "input"
						}
						if part != "system" || provider != "openai_resp" {
							if items, ok := obj[key].([]any); !ok || len(items) != 0 {
								t.Errorf("expected empty %s array: %s", key, data)
							}
						}
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"input_tokens":0,"totalTokens":0}`)), Request: r}, nil
				})
				original, originalTransport := httputil.DefaultClient, http.DefaultTransport
				httputil.DefaultClient = &http.Client{Transport: transport}
				http.DefaultTransport = transport
				t.Cleanup(func() { httputil.DefaultClient = original; http.DefaultTransport = originalTransport })
				c := New(Config{Provider: provider, AnthropicAPIKey: "key", GeminiAPIKey: "key", OpenAIAPIKey: "key", AnthropicModel: "claude-sonnet-5-5", GeminiModel: "gemini-3.8-flash", OpenAIModel: "gpt-6-sol"})
				var opts []chat.Option
				switch part {
				case "tools":
					opts = append(opts, chat.WithTools([]chat.Tool{chat.FunctionTool("tool", "desc", []byte(`{"type":"object","properties":{}}`))}))
				case "system":
					opts = append(opts, chat.WithMessages(chat.System("prefix")))
				case "history prefix":
					opts = append(opts, chat.WithMessages(chat.User("hi"), chat.Assistant("hello")))
				}
				got, err := c.CountTokens(context.Background(), opts...)
				if err != nil || got == nil || got.InputTokens != 0 {
					t.Fatalf("count=%+v err=%v", got, err)
				}
			})
		}
	}
}

func TestCountTokensUnsupportedAndCancellation(t *testing.T) {
	c := New(Config{})
	for _, provider := range []string{"", "openai", "deepseek", "xai", "groq", "cloudflare", "azure", "claude_oauth", "xai_oauth", "unknown"} {
		if c.SupportsCountTokens(provider) {
			t.Errorf("unexpected support: %q", provider)
		}
		if _, err := c.CountTokens(context.Background(), chat.WithProvider(provider)); !errors.Is(err, ErrTokenCountUnsupported) {
			t.Errorf("%s: %v", provider, err)
		}
	}
	for _, provider := range []string{"anthropic", "gemini", "openai_resp", "bedrock", "openai_codex"} {
		if !c.SupportsCountTokens(provider) {
			t.Errorf("missing support: %s", provider)
		}
	}
	c = New(Config{Provider: "openai_codex", CodexSubscription: &rootCredentialSource{}})
	if c.SupportsCountTokens("") {
		t.Error("subscription counting unsupported")
	}
	if _, err := c.CountTokens(context.Background()); !errors.Is(err, ErrTokenCountUnsupported) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(Config{Provider: "anthropic"}).CountTokens(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := chat.BuildRequest(); err == nil {
		t.Error("Chat must still require messages")
	}
}

func TestCountTokensErrors(t *testing.T) {
	for _, provider := range []string{"anthropic", "gemini", "openai_resp"} {
		for _, tt := range []struct {
			status      int
			body        string
			unsupported bool
		}{
			{200, `{}`, false}, {200, `null`, false}, {200, `{"input_tokens":-1,"totalTokens":-1}`, false}, {200, `{"input_tokens":1.5,"totalTokens":1.5}`, false}, {401, `{"error":{"message":"denied"}}`, false}, {429, `{"error":{"message":"limited"}}`, false}, {404, `{"error":{"message":"model not found"}}`, false}, {405, `method not allowed`, true}, {501, `not implemented`, true},
		} {
			t.Run(fmt.Sprintf("%s/%d/%s", provider, tt.status, tt.body), func(t *testing.T) {
				transport := rootRoundTripFunc(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: tt.status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(tt.body)), Request: r}, nil
				})
				original, originalTransport := httputil.DefaultClient, http.DefaultTransport
				httputil.DefaultClient = &http.Client{Transport: transport}
				http.DefaultTransport = transport
				t.Cleanup(func() { httputil.DefaultClient = original; http.DefaultTransport = originalTransport })
				c := New(Config{Provider: provider, AnthropicAPIKey: "key", GeminiAPIKey: "key", OpenAIAPIKey: "key", AnthropicModel: "claude-sonnet-5-5", GeminiModel: "gemini-3.8-flash", OpenAIModel: "gpt-6-sol"})
				got, err := c.CountTokens(context.Background(), chat.WithMessages(chat.User("hi")))
				if err == nil || got != nil || errors.Is(err, ErrTokenCountUnsupported) != tt.unsupported {
					t.Fatalf("count=%+v err=%v", got, err)
				}
			})
		}
	}
}

func TestCountTokensRejectsUnsafeRedirects(t *testing.T) {
	for _, provider := range []string{"anthropic", "gemini"} {
		t.Run(provider, func(t *testing.T) {
			calls := 0
			original := httputil.DefaultClient
			t.Cleanup(func() { httputil.DefaultClient = original })
			httputil.DefaultClient = &http.Client{Transport: rootRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return &http.Response{StatusCode: 307, Header: http.Header{"Location": {"https://unrelated.example/count"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"input_tokens":10,"totalTokens":10}`)), Request: r}, nil
			})}
			c := New(Config{Provider: provider, AnthropicAPIKey: "key", GeminiAPIKey: "key", AnthropicModel: "claude-sonnet-5-5", GeminiModel: "gemini-3.8-flash"})
			if _, err := c.CountTokens(context.Background(), chat.WithMessages(chat.User("hi"))); err == nil || calls != 1 {
				t.Fatalf("redirect not blocked: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestCountTokensModesAndValidation(t *testing.T) {
	original := httputil.DefaultClient
	t.Cleanup(func() { httputil.DefaultClient = original })
	httputil.DefaultClient = &http.Client{Transport: rootRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached network")
		return nil, nil
	})}
	c := New(Config{Provider: "anthropic", AnthropicAPIKey: "key", AnthropicModel: "claude-sonnet-5-5"})
	tool := chat.FunctionTool("tool", "desc", []byte(`{"type":"object"}`))
	for _, mode := range []chat.ToolsEmulationMode{chat.ToolsEmulationForce, chat.ToolsEmulationFallback} {
		if _, err := c.CountTokens(context.Background(), chat.WithTools([]chat.Tool{tool}), chat.WithToolsEmulationMode(mode)); !errors.Is(err, ErrTokenCountUnsupported) {
			t.Fatal(err)
		}
	}
	if _, err := c.CountTokens(context.Background(), chat.WithTools([]chat.Tool{chat.WithToolCacheControl(tool, chat.CacheControl{TTL: "invalid"})})); err == nil || errors.Is(err, ErrTokenCountUnsupported) {
		t.Fatalf("incorrect validation error: %v", err)
	}
	// Inference remains strict even though the shared mapping accepts partial inputs.
	if _, err := c.Chat(context.Background(), chat.WithMessages(chat.System("only system"))); err == nil {
		t.Error("Chat accepted a partial request")
	}
}

func TestCountTokensTransportError(t *testing.T) {
	for _, provider := range []string{"anthropic", "gemini", "openai_resp"} {
		t.Run(provider, func(t *testing.T) {
			failure := errors.New("transport failed")
			transport := rootRoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, failure })
			original, originalTransport := httputil.DefaultClient, http.DefaultTransport
			httputil.DefaultClient = &http.Client{Transport: transport}
			http.DefaultTransport = transport
			t.Cleanup(func() { httputil.DefaultClient = original; http.DefaultTransport = originalTransport })
			c := New(Config{Provider: provider, AnthropicAPIKey: "key", GeminiAPIKey: "key", OpenAIAPIKey: "key", AnthropicModel: "claude-sonnet-5-5", GeminiModel: "gemini-3.8-flash", OpenAIModel: "gpt-6-sol"})
			_, err := c.CountTokens(context.Background())
			if !errors.Is(err, failure) || errors.Is(err, ErrTokenCountUnsupported) {
				t.Fatalf("lost transport error: %v", err)
			}
		})
	}
}
