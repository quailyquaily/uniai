package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/chat"
)

func TestFinishReasonMapping(t *testing.T) {
	tests := []struct {
		name, reason, want string
		tool, wantErr      bool
	}{
		{name: "legacy missing reason"},
		{name: "end turn", reason: "end_turn", want: "stop"},
		{name: "stop sequence", reason: "stop_sequence", want: "stop"},
		{name: "token limit", reason: "max_tokens", want: "length"},
		{name: "context limit", reason: "model_context_window_exceeded", want: "length"},
		{name: "tool call", reason: "tool_use", want: "tool_calls", tool: true},
		{name: "truncated tool call", reason: "max_tokens", want: "length", tool: true},
		{name: "refusal", reason: "refusal", want: "content_filter"},
		{name: "unsupported server tool continuation", reason: "pause_turn", wantErr: true},
		{name: "unknown reason", reason: "future_reason", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Run("json", func(t *testing.T) {
				content := anthropicContentPart{Type: "text", Text: "{"}
				if tt.tool {
					content = anthropicContentPart{Type: "tool_use", ID: "call_1", Name: "lookup", Input: json.RawMessage(`{}`)}
				}
				result, err := toResult(&anthropicResponse{StopReason: tt.reason, Content: []anthropicContentPart{content}}, false)
				if tt.wantErr {
					if err == nil || !strings.Contains(err.Error(), "stop_reason") {
						t.Fatalf("expected stop_reason error, got result=%+v err=%v", result, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if result.FinishReason != tt.want {
					t.Fatalf("reason=%q, want %q", result.FinishReason, tt.want)
				}
				if tt.tool && len(result.ToolCalls) != 1 {
					t.Fatalf("lost tool call: %+v", result)
				}
			})
			for _, subscription := range []bool{false, true} {
				t.Run(fmt.Sprintf("stream/subscription=%t", subscription), func(t *testing.T) {
					p := &Provider{}
					if subscription {
						p.cfg.CredentialSource = &subscriptionSource{}
					}
					content := sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"{"}}`)
					args := `{"n":1}`
					if tt.reason == "max_tokens" {
						args = `{"n":`
					}
					if tt.tool {
						content = sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"lookup"}}`) +
							sseEvent("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":%q}}`, args))
					}
					stream := sseEvent("message_start", `{"type":"message_start","message":{"model":"claude-sonnet-4-6","usage":{"input_tokens":2}}}`) + content +
						sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`) +
						sseEvent("message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q},"usage":{"output_tokens":1}}`, tt.reason)) +
						// A later usage-only delta must not clear the captured reason.
						sseEvent("message_delta", `{"type":"message_delta","usage":{"output_tokens":2}}`) +
						sseEvent("message_stop", `{"type":"message_stop"}`)
					var done []chat.StreamEvent
					result, err := p.chatStream(strings.NewReader(stream), false, func(ev chat.StreamEvent) error {
						if ev.Done {
							done = append(done, ev)
						} else if ev.FinishReason != "" {
							t.Errorf("premature finish reason: %+v", ev)
						}
						return nil
					})
					if tt.wantErr {
						if err == nil || len(done) != 0 || !strings.Contains(err.Error(), "stop_reason") {
							t.Fatalf("done=%+v err=%v", done, err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if result.FinishReason != tt.want || len(done) != 1 || done[0].FinishReason != tt.want {
						t.Fatalf("result=%+v done=%+v want=%q", result, done, tt.want)
					}
					if result.Usage.OutputTokens != 2 || done[0].Usage == nil || done[0].Usage.OutputTokens != 2 {
						t.Fatalf("usage was lost: result=%+v done=%+v", result, done)
					}
					if tt.tool && (len(result.ToolCalls) != 1 || result.ToolCalls[0].Function.Arguments != args) {
						t.Fatalf("tool arguments were lost or repaired: %+v", result.ToolCalls)
					}
				})
			}
		})
	}
}
