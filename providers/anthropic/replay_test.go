package anthropic

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/chat"
)

func TestSonnet55SignedContentReplay(t *testing.T) {
	content := `[{"type":"text","text":"Checking. "},{"type":"thinking","thinking":"","signature":"signed-empty"},{"type":"thinking","thinking":" inspect ","signature":"signed-summary"},{"type":"redacted_thinking","data":"opaque"},{"type":"tool_use","id":"call_1","name":"lookup","input":{"key":"value"}}]`
	for _, stream := range []bool{false, true} {
		for _, details := range []bool{false, true} {
			var result *chat.Result
			var err error
			if stream {
				var blocks []map[string]any
				if err := json.Unmarshal([]byte(content), &blocks); err != nil {
					t.Fatal(err)
				}
				events := sseEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-5-5"}}`)
				for index, block := range blocks {
					start := make(map[string]any)
					for k, v := range block {
						start[k] = v
					}
					var deltas []map[string]any
					switch block["type"] {
					case "thinking":
						start["thinking"], start["signature"] = "", ""
						deltas = append(deltas, map[string]any{"type": "thinking_delta", "thinking": block["thinking"]}, map[string]any{"type": "signature_delta", "signature": block["signature"]})
					case "tool_use":
						start["input"] = map[string]any{}
						deltas = append(deltas, map[string]any{"type": "input_json_delta", "partial_json": `{"key":`}, map[string]any{"type": "input_json_delta", "partial_json": `"value"}`})
					}
					data, _ := json.Marshal(map[string]any{"type": "content_block_start", "index": index, "content_block": start})
					events += sseEvent("content_block_start", string(data))
					for _, delta := range deltas {
						data, _ = json.Marshal(map[string]any{"type": "content_block_delta", "index": index, "delta": delta})
						events += sseEvent("content_block_delta", string(data))
					}
					data, _ = json.Marshal(map[string]any{"type": "content_block_stop", "index": index})
					events += sseEvent("content_block_stop", string(data))
				}
				events += sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`) + sseEvent("message_stop", `{"type":"message_stop"}`)
				result, err = (&Provider{}).chatStream(strings.NewReader(events), details, func(event chat.StreamEvent) error {
					if !details && event.ReasoningDelta != nil {
						t.Error("unexpected reasoning delta")
					}
					return nil
				})
			} else {
				var response anthropicResponse
				if err := json.Unmarshal([]byte(`{"model":"claude-sonnet-5-5","stop_reason":"tool_use","content":`+content+`}`), &response); err != nil {
					t.Fatal(err)
				}
				result, err = toResult(&response, details)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !details && result.Reasoning != nil {
				t.Error("unexpected reasoning details")
			}
			history := append([]chat.Message{chat.User("look up value")}, chat.AssistantReplayMessages(result)...)
			history = append(history, chat.ToolResult("call_1", "found"))
			body, err := buildRequest(&chat.Request{Messages: history}, "claude-sonnet-5-5")
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(body.Messages[1])
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Content any `json:"content"`
			}
			var want any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(content), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Content, want) {
				t.Errorf("stream=%t details=%t: replay changed content: %s", stream, details, data)
			}
			before := string(result.Messages[0].AnthropicContent)
			history[1].AnthropicContent[0] = ' '
			if string(result.Messages[0].AnthropicContent) != before {
				t.Error("replay history aliases result content")
			}
		}
	}
}
