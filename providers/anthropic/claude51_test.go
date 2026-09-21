package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
)

func TestClaude51ToolChoice(t *testing.T) {
	for _, model := range []string{"claude-fable-5-1", "claude-mythos-5-1", "anthropic/claude-fable-5.1", "claude-fable-5", "claude-sonnet-5"} {
		for _, choice := range []chat.ToolChoice{chat.ToolChoiceAuto(), chat.ToolChoiceNone(), chat.ToolChoiceRequired(), chat.ToolChoiceFunction("lookup")} {
			req := &chat.Request{Messages: []chat.Message{chat.User("hello")}, Tools: []chat.Tool{chat.FunctionTool("lookup", "", nil)}, ToolChoice: &choice}
			body, err := buildRequest(req, model)
			wantError := (strings.Contains(model, "5-1") || strings.Contains(model, "5.1")) && (choice.Mode == "required" || choice.Mode == "function")
			if wantError {
				if err == nil || !strings.Contains(err.Error(), "tool_choice") {
					t.Errorf("%s %s: expected tool_choice error, got %v", model, choice.Mode, err)
				}
			} else if err != nil || body.ToolChoice == nil {
				t.Errorf("%s %s: unexpected result %v, %v", model, choice.Mode, body, err)
			}
		}
	}
}

func TestClaude51ReasoningDetails(t *testing.T) {
	temperature, topP := 0.7, 0.9
	for _, model := range []string{"claude-fable-5-1", "claude-mythos-5-1"} {
		for _, effort := range []chat.ReasoningEffort{"low", "medium", "high", "xhigh", "max"} {
			req := &chat.Request{Messages: []chat.Message{chat.User("hello")}, Options: chat.Options{ReasoningEffort: &effort, ReasoningDetails: true, Temperature: &temperature, TopP: &topP, Anthropic: structs.JSONMap{"top_k": 10}}}
			body, err := buildRequest(req, model)
			if err != nil {
				t.Fatal(err)
			}
			if body.Thinking == nil || body.Thinking.Type != "adaptive" || body.Thinking.Display != "summarized" {
				t.Errorf("%s: missing summarized thinking: %#v", model, body.Thinking)
			}
			if body.OutputConfig == nil || body.OutputConfig.Effort != string(effort) {
				t.Errorf("missing effort: %#v", body.OutputConfig)
			}
			if body.Temperature != nil || body.TopP != nil || body.TopK != nil {
				t.Error("sampling parameters were retained")
			}
		}
	}
}

func TestClaude51StrictTools(t *testing.T) {
	for _, strict := range []*bool{nil, new(bool), func() *bool { v := true; return &v }()} {
		tool := chat.FunctionTool("lookup", "", []byte(`{"type":"object","properties":{},"additionalProperties":false}`))
		tool.Function.Strict = strict
		body, err := buildRequest(&chat.Request{Messages: []chat.Message{chat.User("hello")}, Tools: []chat.Tool{tool}}, "claude-fable-5-1")
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(body.Tools[0])
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatal(err)
		}
		got, exists := payload["strict"]
		if strict == nil {
			if exists {
				t.Errorf("unexpected strict: %s", data)
			}
		} else if !exists || got != *strict {
			t.Errorf("lost explicit strict: %s", data)
		}
	}
}
