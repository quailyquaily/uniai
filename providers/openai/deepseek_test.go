package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
)

func TestDeepSeekFlashDisablesThinking(t *testing.T) {
	for _, model := range []string{"deepseek-flash", "deepseek-v4-flash", "deepseek-v4-flash-vision-exp", "deepseek-v4-pro"} {
		for _, raw := range []bool{false, true} {
			effort := chat.ReasoningEffortNone
			req := &chat.Request{Model: model, Messages: []chat.Message{chat.User("hello")}}
			if raw {
				req.Options.OpenAI = structs.JSONMap{"reasoning_effort": "none"}
			} else {
				req.Options.ReasoningEffort = &effort
			}
			params, err := buildParams(req, "")
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(params)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), `"thinking":{"type":"disabled"}`) || strings.Contains(string(data), `"reasoning_effort"`) {
				t.Errorf("%s raw=%v: wrong thinking toggle: %s", model, raw, data)
			}
		}
	}
}

func TestDeepSeekFlashPreservesVisionAndToolReasoning(t *testing.T) {
	effort := chat.ReasoningEffortHigh
	req := &chat.Request{
		Model: "deepseek-flash",
		Messages: []chat.Message{
			chat.UserParts(chat.TextPart("describe"), chat.ImageURLPart("https://example.com/image.png")),
			{Role: chat.RoleAssistant, Content: "checking", ReasoningContent: "saved reasoning", ToolCalls: []chat.ToolCall{{ID: "call_1", Type: "function", Function: chat.ToolCallFunction{Name: "lookup", Arguments: "{}"}}}},
			{Role: chat.RoleTool, ToolCallID: "call_1", Content: "result"},
		},
		Tools:   []chat.Tool{chat.FunctionTool("lookup", "", nil)},
		Options: chat.Options{ReasoningEffort: &effort},
	}
	params, err := buildParams(req, "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"reasoning_effort":"high"`, `"reasoning_content":"saved reasoning"`, `"type":"image_url"`, `"tool_call_id":"call_1"`} {
		if !strings.Contains(string(data), field) {
			t.Errorf("missing %s in %s", field, data)
		}
	}
	if len(params.Tools) != 1 {
		t.Errorf("missing tools: %s", data)
	}
}
