package uniai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/chat"
)

func TestParseToolDecisionSingle(t *testing.T) {
	calls, err := parseToolDecision(`{"tool":"get_weather","arguments":{"city":"Tokyo"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "get_weather" {
		t.Fatalf("unexpected tool name: %s", calls[0].Name)
	}
	assertArgs(t, calls[0].Arguments, map[string]any{"city": "Tokyo"})
}

func TestParseToolDecisionMultiple(t *testing.T) {
	input := `{"tools":[{"tool":"a","arguments":{"x":1}},{"tool":"b","arguments":{}}]}`
	calls, err := parseToolDecision(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Name != "a" || calls[1].Name != "b" {
		t.Fatalf("unexpected tool order: %s, %s", calls[0].Name, calls[1].Name)
	}
	assertArgs(t, calls[0].Arguments, map[string]any{"x": float64(1)})
	assertArgs(t, calls[1].Arguments, map[string]any{})
}

func TestParseToolDecisionInvalidTool(t *testing.T) {
	_, err := parseToolDecision(`{"tool":123,"arguments":{}}`)
	if err == nil {
		t.Fatalf("expected error for invalid tool type")
	}
}

func TestBuildToolDecisionPrompt(t *testing.T) {
	req := &chat.Request{
		Tools: []chat.Tool{
			FunctionTool("get_weather", "Get weather", []byte(`{
				"type":"object",
				"properties":{"city":{"type":"string"}},
				"required":["city"]
			}`)),
		},
		ToolChoice: &chat.ToolChoice{Mode: "required"},
	}
	prompt, err := buildToolDecisionPrompt(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "get_weather") {
		t.Fatalf("prompt missing tool name")
	}
	if !strings.Contains(prompt, "MUST return at least one tool") {
		t.Fatalf("prompt missing required tool instruction")
	}
}

func TestEnforceToolChoice(t *testing.T) {
	err := enforceToolChoice(&chat.ToolChoice{Mode: "none"}, []emulatedToolCall{{Name: "a"}})
	if err == nil {
		t.Fatalf("expected error for tool_choice none")
	}
	err = enforceToolChoice(&chat.ToolChoice{Mode: "required"}, nil)
	if err == nil {
		t.Fatalf("expected error for tool_choice required with no calls")
	}
	err = enforceToolChoice(&chat.ToolChoice{Mode: "function", FunctionName: "a"}, []emulatedToolCall{{Name: "b"}})
	if err == nil {
		t.Fatalf("expected error for tool_choice function mismatch")
	}
}

func assertArgs(t *testing.T, raw json.RawMessage, expected map[string]any) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("failed to unmarshal args: %v", err)
	}
	if len(got) != len(expected) {
		t.Fatalf("args size mismatch: got=%v expected=%v", got, expected)
	}
	for k, v := range expected {
		if got[k] != v {
			t.Fatalf("args mismatch for %s: got=%v expected=%v", k, got[k], v)
		}
	}
}
