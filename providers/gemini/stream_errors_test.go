package gemini

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/chat"
)

func TestChatStreamCompletionStates(t *testing.T) {
	for _, tc := range []struct{ name, payload, finish, wantErr string }{
		{name: "stop", payload: `{"candidates":[{"content":{"parts":[{"text":"answer"}]},"finishReason":"STOP"}]}`, finish: "stop"},
		{name: "length", payload: `{"candidates":[{"content":{"parts":[{"text":"partial"}]},"finishReason":"MAX_TOKENS"}]}`, finish: "length"},
		{name: "tool call", payload: `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{}}}]},"finishReason":"STOP"}]}`, finish: "tool_calls"},
		{name: "safety", payload: `{"candidates":[{"finishReason":"SAFETY"}]}`, finish: "content_filter"},
		{name: "blocked prompt", payload: `{"promptFeedback":{"blockReason":"SAFETY"}}`, finish: "content_filter"},
		{name: "blocked prompt overrides candidate error", payload: `{"promptFeedback":{"blockReason":"SAFETY"},"candidates":[{"finishReason":"MALFORMED_FUNCTION_CALL"}]}`, finish: "content_filter"},
		{name: "unspecified finish reason", payload: `{"candidates":[{"finishReason":"FINISH_REASON_UNSPECIFIED"}]}`, wantErr: "gemini stream ended without a finish reason"},
		{name: "empty prompt feedback", payload: `{"promptFeedback":{}}`, wantErr: "gemini stream ended without a finish reason"},
		{name: "unspecified block reason", payload: `{"promptFeedback":{"blockReason":"BLOCK_REASON_UNSPECIFIED"}}`, wantErr: "gemini stream ended without a finish reason"},
		{name: "malformed tool call", payload: `{"candidates":[{"finishReason":"MALFORMED_FUNCTION_CALL"}]}`, wantErr: "MALFORMED_FUNCTION_CALL"},
		{name: "early EOF", payload: `{"candidates":[{"content":{"parts":[{"text":"partial"}]}}]}`, wantErr: "finish"},
		{name: "empty", wantErr: "finish"},
		{name: "invalid JSON", payload: `{bad}`, wantErr: "response"},
		{name: "event error", payload: `{"error":{"message":"quota exceeded"}}`, wantErr: "quota exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := ""
			if tc.payload != "" {
				stream = "data: " + tc.payload + "\n\n"
			}
			var done []chat.StreamEvent
			result, err := (&Provider{}).chatStream(strings.NewReader(stream), "custom-model", false, func(ev chat.StreamEvent) error {
				if ev.Done {
					done = append(done, ev)
				}
				return nil
			})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || result != nil || len(done) != 0 {
					t.Fatalf("result=%+v done=%+v err=%v, want %q", result, done, err, tc.wantErr)
				}
				return
			}
			if err != nil || result == nil || result.FinishReason != tc.finish || len(done) != 1 || done[0].FinishReason != tc.finish {
				t.Fatalf("result=%+v done=%+v err=%v, want %q", result, done, err, tc.finish)
			}
			var response geminiResponse
			if err := json.Unmarshal([]byte(tc.payload), &response); err != nil {
				t.Fatal(err)
			}
			blocking, err := toChatResult(&response, "custom-model", false)
			if err != nil || blocking.FinishReason != tc.finish {
				t.Fatalf("blocking result=%+v err=%v", blocking, err)
			}
		})
	}
}

func TestChatStreamPreservesUsageAfterFinishReason(t *testing.T) {
	stream := "data:{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"answer\"}]},\"finishReason\":\"STOP\"}]}\n\n" +
		"data: {\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":2,\"totalTokenCount\":5}}\n\n"
	result, err := (&Provider{}).chatStream(strings.NewReader(stream), "custom-model", false, func(chat.StreamEvent) error { return nil })
	if err != nil || result.FinishReason != "stop" || result.Usage.TotalTokens != 5 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
