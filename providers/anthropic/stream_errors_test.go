package anthropic

import (
	"io"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/chat"
)

func TestChatStreamRejectsInvalidOrUnfinishedEvents(t *testing.T) {
	stop := sseEvent("message_stop", `{"type":"message_stop"}`)
	for _, tc := range []struct{ name, stream, want string }{
		{"empty", "", "message_stop"},
		{"early EOF", sseEvent("content_block_delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"partial"}}`), "message_stop"},
		{"upstream error", sseEvent("error", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`) + stop, "Overloaded"},
		{"invalid JSON", sseEvent("content_block_delta", `{bad}`) + stop, "invalid"},
		{"invalid delta", sseEvent("content_block_delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":123}}`) + stop, "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			done := false
			result, err := (&Provider{}).chatStream(strings.NewReader(tc.stream), false, func(ev chat.StreamEvent) error { done = done || ev.Done; return nil })
			if err == nil || !strings.Contains(err.Error(), tc.want) || result != nil || done {
				t.Fatalf("result=%+v done=%t err=%v, want error containing %q", result, done, err, tc.want)
			}
		})
	}
}

func TestChatStreamStopsAtMessageStop(t *testing.T) {
	for _, compact := range []bool{false, true} {
		stream := sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`) + sseEvent("message_stop", `{"type":"message_stop"}`)
		if compact {
			stream = strings.ReplaceAll(strings.ReplaceAll(stream, "event: ", "event:"), "data: ", "data:")
		}
		done := false
		result, err := (&Provider{}).chatStream(io.MultiReader(strings.NewReader(stream), afterCompletionReader{}), false, func(ev chat.StreamEvent) error { done = done || ev.Done; return nil })
		if err != nil || !done || result == nil || result.FinishReason != "stop" {
			t.Fatalf("compact=%t result=%+v done=%t err=%v", compact, result, done, err)
		}
	}
}
