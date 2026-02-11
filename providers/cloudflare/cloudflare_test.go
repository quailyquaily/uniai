package cloudflare

import (
	"encoding/json"
	"testing"

	"github.com/quailyquaily/uniai/chat"
)

func TestBuildPayloadMapsResponsesToolsForGptOss(t *testing.T) {
	strict := true
	temp := 0.2
	req := &chat.Request{
		Model: "@cf/openai/gpt-oss-120b",
		Messages: []chat.Message{
			chat.User("What's the weather in Tokyo?"),
		},
		Options: chat.Options{
			Temperature: &temp,
		},
		Tools: []chat.Tool{
			{
				Type: "function",
				Function: chat.ToolFunction{
					Name:                 "get_weather",
					Description:          "Get weather",
					ParametersJSONSchema: []byte(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
					Strict:               &strict,
				},
			},
		},
		ToolChoice: func() *chat.ToolChoice {
			c := chat.ToolChoiceFunction("get_weather")
			return &c
		}(),
	}

	payload, err := buildPayload(req, req.Model)
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	if _, ok := payload["input"]; !ok {
		t.Fatalf("expected input to be set for gpt-oss")
	}
	tools, ok := payload["tools"].([]map[string]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected one mapped tool, got %#v", payload["tools"])
	}
	if tools[0]["type"] != "function" {
		t.Fatalf("expected responses tool type=function, got %#v", tools[0]["type"])
	}
	if tools[0]["name"] != "get_weather" {
		t.Fatalf("unexpected tool name: %#v", tools[0]["name"])
	}
	if tools[0]["strict"] != true {
		t.Fatalf("expected strict=true")
	}
	if _, ok := tools[0]["parameters"].(map[string]any); !ok {
		t.Fatalf("expected parameters schema map")
	}

	choice, ok := payload["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("expected function tool_choice map, got %#v", payload["tool_choice"])
	}
	if choice["type"] != "function" || choice["name"] != "get_weather" {
		t.Fatalf("unexpected tool_choice: %#v", choice)
	}
}

func TestBuildPayloadMapsTraditionalToolsAndToolMessages(t *testing.T) {
	req := &chat.Request{
		Model: "@cf/meta/llama-4-scout",
		Messages: []chat.Message{
			chat.User("weather?"),
			{
				Role: chat.RoleAssistant,
				ToolCalls: []chat.ToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: chat.ToolCallFunction{
							Name:      "get_weather",
							Arguments: `{"city":"Tokyo"}`,
						},
					},
				},
			},
			chat.ToolResult("call_1", `{"temperature_c":18}`),
		},
		Tools: []chat.Tool{
			chat.FunctionTool("get_weather", "Get weather", []byte(`{"type":"object","properties":{"city":{"type":"string"}}}`)),
		},
		ToolChoice: func() *chat.ToolChoice {
			c := chat.ToolChoiceRequired()
			return &c
		}(),
	}

	payload, err := buildPayload(req, req.Model)
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	if _, ok := payload["messages"]; !ok {
		t.Fatalf("expected messages to be set for non gpt-oss")
	}
	if _, ok := payload["tool_choice"]; ok {
		t.Fatalf("did not expect tool_choice mapping for non gpt-oss")
	}

	tools, ok := payload["tools"].([]map[string]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected one mapped tool, got %#v", payload["tools"])
	}
	if _, hasType := tools[0]["type"]; hasType {
		t.Fatalf("traditional cloudflare tools should not include type field")
	}
	if tools[0]["name"] != "get_weather" {
		t.Fatalf("unexpected tool name: %#v", tools[0]["name"])
	}

	messages, ok := payload["messages"].([]map[string]any)
	if !ok || len(messages) != 3 {
		t.Fatalf("expected 3 mapped messages, got %#v", payload["messages"])
	}
	assistantContent, ok := messages[1]["content"].(string)
	if !ok || assistantContent == "" {
		t.Fatalf("expected assistant tool-call content string, got %#v", messages[1]["content"])
	}
	var assistantTool map[string]any
	if err := json.Unmarshal([]byte(assistantContent), &assistantTool); err != nil {
		t.Fatalf("assistant tool-call content must be JSON: %v", err)
	}
	if assistantTool["name"] != "get_weather" {
		t.Fatalf("unexpected assistant tool-call content: %#v", assistantTool)
	}
}

func TestParseToolCallsSupportsTraditionalAndResponsesShapes(t *testing.T) {
	calls := parseToolCalls([]any{
		map[string]any{
			"name": "get_weather",
			"arguments": map[string]any{
				"city": "Tokyo",
			},
		},
	})
	if len(calls) != 1 {
		t.Fatalf("expected one traditional tool call, got %d", len(calls))
	}
	if calls[0].Type != "function" || calls[0].Function.Name != "get_weather" {
		t.Fatalf("unexpected traditional call: %#v", calls[0])
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(calls[0].Function.Arguments), &args); err != nil {
		t.Fatalf("invalid traditional call args json: %v", err)
	}
	if args["city"] != "Tokyo" {
		t.Fatalf("unexpected traditional call args: %#v", args)
	}

	outCalls := extractToolCalls(map[string]any{
		"output": []any{
			map[string]any{
				"type":    "function_call",
				"call_id": "call_123",
				"name":    "get_weather",
				"arguments": map[string]any{
					"city": "Tokyo",
				},
			},
		},
	})
	if len(outCalls) != 1 {
		t.Fatalf("expected one output tool call, got %d", len(outCalls))
	}
	if outCalls[0].ID != "call_123" || outCalls[0].Function.Name != "get_weather" {
		t.Fatalf("unexpected output call: %#v", outCalls[0])
	}
}
