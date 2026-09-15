package uniai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientReasoningDetailsFromResponseFields(t *testing.T) {
	for _, tc := range []struct {
		provider string
		response string
		stream   []string
		remove   string
		kind     ReasoningDeltaType
	}{
		{
			provider: "openai",
			response: `{"model":"deployment-alias","choices":[{"message":{"role":"assistant","reasoning_content":"inspect","content":"answer"}}]}`,
			stream: []string{
				`{"id":"test","object":"chat.completion.chunk","model":"deployment-alias","choices":[{"index":0,"delta":{"reasoning_content":"inspect"}}]}`,
				`{"id":"test","object":"chat.completion.chunk","model":"deployment-alias","choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":"stop"}]}`,
			},
			remove: `"reasoning_content":"inspect",`,
			kind:   ReasoningDeltaThinking,
		},
		{
			provider: "azure",
			response: `{"model":"deployment-alias","choices":[{"message":{"role":"assistant","reasoning_content":"inspect","content":"answer"}}]}`,
			stream: []string{
				`{"id":"test","object":"chat.completion.chunk","model":"deployment-alias","choices":[{"index":0,"delta":{"reasoning_content":"inspect"}}]}`,
				`{"id":"test","object":"chat.completion.chunk","model":"deployment-alias","choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":"stop"}]}`,
			},
			remove: `"reasoning_content":"inspect",`,
			kind:   ReasoningDeltaThinking,
		},
		{
			provider: "gemini",
			response: `{"modelVersion":"deployment-alias","candidates":[{"content":{"role":"model","parts":[{"thought":true,"text":"inspect"},{"text":"answer"}]},"finishReason":"STOP"}]}`,
			stream: []string{
				`{"modelVersion":"deployment-alias","candidates":[{"content":{"role":"model","parts":[{"thought":true,"text":"inspect"}]}}]}`,
				`{"modelVersion":"deployment-alias","candidates":[{"content":{"role":"model","parts":[{"text":"answer"}]},"finishReason":"STOP"}]}`,
			},
			remove: `{"thought":true,"text":"inspect"},`,
			kind:   ReasoningDeltaSummary,
		},
		{
			provider: "anthropic",
			response: `{"model":"deployment-alias","content":[{"type":"thinking","thinking":"inspect"},{"type":"text","text":"answer"}],"stop_reason":"end_turn"}`,
			stream: []string{
				`{"type":"message_start","message":{"model":"deployment-alias"}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"inspect"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
				`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"answer"}}`,
				`{"type":"content_block_stop","index":1}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
				`{"type":"message_stop"}`,
			},
			remove: `{"type":"thinking","thinking":"inspect"},`,
			kind:   ReasoningDeltaThinking,
		},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			for _, mode := range []string{"blocking", "streaming", "blocking_sse"} {
				if mode == "blocking_sse" && tc.provider != "openai" {
					continue
				}
				for _, details := range []bool{false, true} {
					for _, present := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/details=%t/present=%t", mode, details, present), func(t *testing.T) {
							server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								if mode == "blocking" {
									w.Header().Set("Content-Type", "application/json")
									body := tc.response
									if !present {
										body = strings.ReplaceAll(body, tc.remove, "")
									}
									fmt.Fprint(w, body)
									return
								}
								w.Header().Set("Content-Type", "text/event-stream")
								for _, event := range tc.stream {
									if !present && (strings.Contains(event, `"inspect"`) || strings.Contains(event, `"thinking"`) || event == `{"type":"content_block_stop","index":0}`) {
										continue
									}
									if tc.provider == "anthropic" {
										var header struct{ Type string }
										if err := json.Unmarshal([]byte(event), &header); err != nil {
											t.Error(err)
											return
										}
										fmt.Fprintf(w, "event: %s\n", header.Type)
									}
									fmt.Fprintf(w, "data: %s\n\n", event)
								}
							}))
							defer server.Close()

							client := New(Config{
								Provider:     tc.provider,
								OpenAIAPIKey: "test-key", OpenAIAPIBase: server.URL + "/v1", OpenAIModel: "deployment-alias",
								AzureOpenAIAPIKey: "test-key", AzureOpenAIEndpoint: server.URL, AzureOpenAIModel: "deployment-alias",
								GeminiAPIKey: "test-key", GeminiAPIBase: server.URL, GeminiModel: "deployment-alias",
								AnthropicAPIKey: "test-key", AnthropicAPIBase: server.URL, AnthropicModel: "deployment-alias",
							})
							opts := []ChatOption{WithMessages(User("hello"))}
							if details {
								opts = append(opts, WithReasoningDetails())
							}
							var deltas []ReasoningDelta
							var text string
							var done bool
							if mode == "streaming" {
								opts = append(opts, WithOnStream(func(event StreamEvent) error {
									text += event.Delta
									if event.ReasoningDelta != nil {
										deltas = append(deltas, *event.ReasoningDelta)
									}
									done = done || event.Done
									return nil
								}))
							}
							result, err := client.Chat(context.Background(), opts...)
							if err != nil {
								t.Fatalf("chat: %v", err)
							}
							if result.Text != "answer" {
								t.Fatalf("unexpected answer: %q", result.Text)
							}
							if details && present {
								if result.Reasoning == nil {
									t.Fatal("returned reasoning was not exposed")
								}
								if tc.kind == ReasoningDeltaSummary {
									if len(result.Reasoning.Summary) != 1 || result.Reasoning.Summary[0] != "inspect" {
										t.Fatalf("unexpected summary: %#v", result.Reasoning)
									}
								} else if len(result.Reasoning.Blocks) != 1 || result.Reasoning.Blocks[0].Text != "inspect" {
									t.Fatalf("unexpected thinking: %#v", result.Reasoning)
								}
							} else if result.Reasoning != nil {
								t.Fatalf("unexpected reasoning: %#v", result.Reasoning)
							}
							if mode == "streaming" {
								if !done || text != "answer" {
									t.Fatalf("incomplete text stream: text=%q done=%t", text, done)
								}
								if details && present {
									if len(deltas) != 1 || deltas[0].Type != tc.kind || deltas[0].Delta != "inspect" {
										t.Fatalf("unexpected reasoning stream: %#v", deltas)
									}
								} else if len(deltas) != 0 {
									t.Fatalf("unexpected reasoning stream: %#v", deltas)
								}
							}
							if tc.provider == "openai" || tc.provider == "azure" {
								want := ""
								if present {
									want = "inspect"
								}
								if len(result.Messages) != 1 || result.Messages[0].ReasoningContent != want {
									t.Fatalf("unexpected replay messages: %#v", result.Messages)
								}
							}
						})
					}
				}
			}
		})
	}
}
