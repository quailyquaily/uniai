package anthropicstream

import (
	"testing"

	"github.com/quailyquaily/uniai/chat"
)

func TestReasoningStateMapsThinkingAndOpaqueFields(t *testing.T) {
	fixtures := []string{
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"inspect"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_1"}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"opaque"}}`,
	}

	var state ReasoningState
	var deltas []chat.ReasoningDelta
	for _, fixture := range fixtures {
		event, err := Decode([]byte(fixture))
		if err != nil {
			t.Fatalf("decode event: %v", err)
		}
		if streamEvent := state.Apply(event, true); streamEvent != nil {
			deltas = append(deltas, *streamEvent.ReasoningDelta)
		}
	}

	if len(deltas) != 1 || deltas[0].Type != chat.ReasoningDeltaThinking || deltas[0].Index != 0 || deltas[0].Delta != "inspect" {
		t.Fatalf("unexpected deltas: %#v", deltas)
	}
	result := state.Result()
	if result == nil || len(result.Blocks) != 2 || result.Blocks[0].Signature != "sig_1" || result.Blocks[1].Data != "opaque" {
		t.Fatalf("unexpected reasoning result: %#v", result)
	}
}

func TestReasoningStateDoesNotCaptureWhenDisabled(t *testing.T) {
	event, err := Decode([]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"inspect"}}`))
	if err != nil {
		t.Fatalf("decode event: %v", err)
	}
	var state ReasoningState
	if streamEvent := state.Apply(event, false); streamEvent != nil {
		t.Fatalf("unexpected stream event: %#v", streamEvent)
	}
	if result := state.Result(); result != nil {
		t.Fatalf("unexpected reasoning result: %#v", result)
	}
}
