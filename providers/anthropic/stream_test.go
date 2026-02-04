package anthropic

import (
	"fmt"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/chat"
)

func sseEvent(event, data string) string {
	return fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
}

func TestChatStreamText(t *testing.T) {
	sse := strings.Join([]string{
		sseEvent("message_start", `{"type":"message_start","message":{"model":"claude-sonnet-4-20250514","usage":{"input_tokens":10}}}`),
		sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`),
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
		sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`),
		sseEvent("message_stop", `{"type":"message_stop"}`),
	}, "")

	var deltas []string
	var gotDone bool
	var gotUsage *chat.Usage

	p := &Provider{}
	result, err := p.chatStream(strings.NewReader(sse), func(ev chat.StreamEvent) error {
		if ev.Done {
			gotDone = true
			gotUsage = ev.Usage
			return nil
		}
		if ev.Delta != "" {
			deltas = append(deltas, ev.Delta)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "Hello world" {
		t.Fatalf("text mismatch: got %q", result.Text)
	}
	if result.Model != "claude-sonnet-4-20250514" {
		t.Fatalf("model mismatch: got %q", result.Model)
	}
	if len(deltas) != 2 || deltas[0] != "Hello" || deltas[1] != " world" {
		t.Fatalf("deltas mismatch: %v", deltas)
	}
	if !gotDone {
		t.Fatal("missing Done event")
	}
	if gotUsage == nil || gotUsage.InputTokens != 10 || gotUsage.OutputTokens != 5 || gotUsage.TotalTokens != 15 {
		t.Fatalf("usage mismatch: %+v", gotUsage)
	}
	if result.Usage.InputTokens != 10 || result.Usage.OutputTokens != 5 {
		t.Fatalf("result usage mismatch: %+v", result.Usage)
	}
}

func TestChatStreamToolCall(t *testing.T) {
	sse := strings.Join([]string{
		sseEvent("message_start", `{"type":"message_start","message":{"model":"claude-sonnet-4-20250514","usage":{"input_tokens":20}}}`),
		sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_01","name":"get_weather"}}`),
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\""}}`),
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":":\"Tokyo\"}"}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
		sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":12}}`),
		sseEvent("message_stop", `{"type":"message_stop"}`),
	}, "")

	var toolDeltas []chat.ToolCallDelta
	p := &Provider{}
	result, err := p.chatStream(strings.NewReader(sse), func(ev chat.StreamEvent) error {
		if ev.ToolCallDelta != nil {
			toolDeltas = append(toolDeltas, *ev.ToolCallDelta)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.ID != "toolu_01" || tc.Function.Name != "get_weather" {
		t.Fatalf("tool call mismatch: %+v", tc)
	}
	if tc.Function.Arguments != `{"city":"Tokyo"}` {
		t.Fatalf("tool args mismatch: %q", tc.Function.Arguments)
	}

	// First delta should carry ID and Name
	if len(toolDeltas) < 1 || toolDeltas[0].ID != "toolu_01" || toolDeltas[0].Name != "get_weather" {
		t.Fatalf("first tool delta mismatch: %+v", toolDeltas)
	}
	// Subsequent deltas should carry args chunks
	if len(toolDeltas) != 3 {
		t.Fatalf("expected 3 tool deltas (start + 2 args), got %d", len(toolDeltas))
	}
	if toolDeltas[1].ArgsChunk != `{"city"` || toolDeltas[2].ArgsChunk != `:"Tokyo"}` {
		t.Fatalf("tool args deltas mismatch: %+v", toolDeltas[1:])
	}
}

func TestChatStreamCallbackError(t *testing.T) {
	sse := strings.Join([]string{
		sseEvent("message_start", `{"type":"message_start","message":{"model":"test","usage":{"input_tokens":1}}}`),
		sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
		sseEvent("message_delta", `{"type":"message_delta","usage":{"output_tokens":1}}`),
		sseEvent("message_stop", `{"type":"message_stop"}`),
	}, "")

	cancelErr := fmt.Errorf("cancelled")
	p := &Provider{}
	_, err := p.chatStream(strings.NewReader(sse), func(ev chat.StreamEvent) error {
		if ev.Delta != "" {
			return cancelErr
		}
		return nil
	})
	if err != cancelErr {
		t.Fatalf("expected cancel error, got: %v", err)
	}
}

func TestChatStreamDoneError(t *testing.T) {
	sse := strings.Join([]string{
		sseEvent("message_start", `{"type":"message_start","message":{"model":"test","usage":{"input_tokens":1}}}`),
		sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
		sseEvent("message_delta", `{"type":"message_delta","usage":{"output_tokens":1}}`),
		sseEvent("message_stop", `{"type":"message_stop"}`),
	}, "")

	doneErr := fmt.Errorf("done error")
	p := &Provider{}
	_, err := p.chatStream(strings.NewReader(sse), func(ev chat.StreamEvent) error {
		if ev.Done {
			return doneErr
		}
		return nil
	})
	if err != doneErr {
		t.Fatalf("expected done error to propagate, got: %v", err)
	}
}
