package anthropic

import (
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
)

func TestHaiku55ThinkingAndTools(t *testing.T) {
	for _, mode := range []string{"", "adaptive", "disabled", "between_tools", "enabled"} {
		for _, effort := range []chat.ReasoningEffort{"low", "medium", "high", "xhigh", "max"} {
			choice := chat.ToolChoiceFunction("lookup")
			req := &chat.Request{Messages: []chat.Message{chat.User("hello")},
				Tools: []chat.Tool{chat.FunctionTool("lookup", "", nil)}, ToolChoice: &choice,
				Options: chat.Options{ReasoningEffort: &effort, ReasoningDetails: true, Anthropic: structs.JSONMap{"thinking_type": mode}},
			}
			body, err := buildRequest(req, "claude-haiku-5-5")
			invalid := mode == "between_tools" || mode == "enabled" || (mode == "disabled" && (effort == "xhigh" || effort == "max"))
			if invalid {
				if err == nil {
					t.Errorf("%s/%s: expected error", mode, effort)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if body.ToolChoice == nil || body.ToolChoice.Type != "tool" {
				t.Fatal("forced tool choice was lost")
			}
			if mode == "disabled" && (body.Thinking.Type != "disabled" || body.Thinking.Display != "") {
				t.Fatalf("disabled thinking: %#v", body.Thinking)
			}
		}
	}
}
