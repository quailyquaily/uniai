package openairesp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/openai/openai-go/v3/responses"
	"github.com/quailyquaily/uniai/chat"
)

func TestBuildParamsOpenAICodexDropsUnsupportedFields(t *testing.T) {
	temperature := 0.2
	maxTokens := 512
	reasoningBudget := 4096
	reasoningEffort := chat.ReasoningEffortHigh
	rawOptions := structs.JSONMap{
		"temperature":             0.8,
		"max_tokens":              1024,
		"max_output_tokens":       2048,
		"prompt_cache_key":        "session-1",
		"prompt_cache_retention":  "24h",
		"prompt_cache_options":    map[string]any{"mode": "explicit", "ttl": "30m"},
		"reasoning_budget_tokens": 8192,
	}
	req := &chat.Request{
		Model: "gpt-4.1",
		Messages: []chat.Message{
			{
				Role: chat.RoleSystem,
				Parts: []chat.Part{
					chat.WithPartCacheControl(chat.TextPart("stable prefix"), chat.CacheControl{}),
				},
			},
			chat.User("answer briefly"),
		},
		Options: chat.Options{
			Temperature:     &temperature,
			MaxTokens:       &maxTokens,
			ReasoningBudget: &reasoningBudget,
			ReasoningEffort: &reasoningEffort,
			OpenAI:          rawOptions,
		},
		Tools: []chat.Tool{
			chat.WithToolCacheControl(
				chat.FunctionTool("lookup", "Look up a value", []byte(`{"type":"object"}`)),
				chat.CacheControl{},
			),
		},
	}

	params, err := buildParams(req, "", true)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	if params.Temperature.Valid() {
		t.Fatalf("temperature must be omitted: %#v", params.Temperature)
	}
	if params.MaxOutputTokens.Valid() {
		t.Fatalf("max_output_tokens must be omitted: %#v", params.MaxOutputTokens)
	}
	if params.Reasoning.Effort != "high" {
		t.Fatalf("reasoning effort = %q, want high", params.Reasoning.Effort)
	}

	payload := marshalResponseParams(t, params)
	for _, key := range []string{
		"temperature",
		"max_tokens",
		"max_output_tokens",
		"prompt_cache_key",
		"prompt_cache_retention",
		"prompt_cache_options",
		"reasoning_budget_tokens",
	} {
		if _, ok := payload[key]; ok {
			t.Fatalf("unexpected %s in payload: %#v", key, payload[key])
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if strings.Contains(string(data), "prompt_cache_breakpoint") {
		t.Fatalf("cache control generated a breakpoint: %s", data)
	}

	for _, key := range []string{
		"temperature",
		"max_tokens",
		"max_output_tokens",
		"prompt_cache_key",
		"prompt_cache_retention",
		"prompt_cache_options",
		"reasoning_budget_tokens",
	} {
		if !rawOptions.HasKey(key) {
			t.Fatalf("caller options were mutated; missing %q", key)
		}
	}
	if req.Messages[0].Parts[0].CacheControl == nil {
		t.Fatalf("caller message cache control was mutated")
	}
	if req.Tools[0].CacheControl == nil {
		t.Fatalf("caller tool cache control was mutated")
	}
}

func TestBuildParamsOpenAICodexRemovesNestedPromptCacheBreakpoints(t *testing.T) {
	content := map[string]any{
		"type": "input_text",
		"text": "Return JSON.",
		"prompt_cache_breakpoint": map[string]any{
			"mode": "explicit",
		},
	}
	rawInput := []any{
		map[string]any{
			"role":    "user",
			"content": []any{content},
		},
	}
	req := &chat.Request{
		Model: "gpt-5.6",
		Options: chat.Options{
			OpenAI: structs.JSONMap{
				"input":           rawInput,
				"response_format": "json_object",
			},
		},
	}

	params, err := buildParams(req, "", true)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if strings.Contains(string(data), "prompt_cache_breakpoint") {
		t.Fatalf("prompt_cache_breakpoint was not removed: %s", data)
	}
	if !strings.Contains(string(data), `"text":"Return JSON."`) {
		t.Fatalf("input text was not preserved: %s", data)
	}
	if _, ok := content["prompt_cache_breakpoint"]; !ok {
		t.Fatalf("caller raw input was mutated")
	}
	items := responsePayloadMessages(t, params)
	if got := len(items); got != 1 {
		t.Fatalf("JSON instruction was added despite existing JSON text: %d input items", got)
	}
}

func TestBuildParamsOpenAICodexAddsJSONInstruction(t *testing.T) {
	tests := []struct {
		name         string
		req          *chat.Request
		originalText string
	}{
		{
			name: "response format string with lowercase json",
			req: &chat.Request{
				Model:    "gpt-5.3-codex",
				Messages: []chat.Message{chat.User("return json only")},
				Options: chat.Options{OpenAI: structs.JSONMap{
					"response_format": "json_object",
				}},
			},
			originalText: "return json only",
		},
		{
			name: "response format object with string input",
			req: &chat.Request{
				Model: "gpt-5.3-codex",
				Options: chat.Options{OpenAI: structs.JSONMap{
					"input":           "list changed files",
					"response_format": map[string]any{"type": "json_object"},
				}},
			},
			originalText: "list changed files",
		},
		{
			name: "native text format",
			req: &chat.Request{
				Model:    "gpt-5.3-codex",
				Messages: []chat.Message{chat.User("list changed files")},
				Options: chat.Options{OpenAI: structs.JSONMap{
					"text": map[string]any{
						"format": map[string]any{"type": "json_object"},
					},
				}},
			},
			originalText: "list changed files",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, err := buildParams(tt.req, "", true)
			if err != nil {
				t.Fatalf("buildParams: %v", err)
			}
			if got := responsePayloadFormatType(t, params); got != "json_object" {
				t.Fatalf("response format = %q, want json_object", got)
			}
			items := responsePayloadMessages(t, params)
			if len(items) != 2 {
				t.Fatalf("input items = %d, want 2", len(items))
			}
			if items[0]["role"] != "user" {
				t.Fatalf("first item is not a user message: %#v", items[0])
			}
			if got := responsePayloadMessageText(items[0]); got != "Return the response as JSON." {
				t.Fatalf("JSON instruction = %q", got)
			}
			if got := responsePayloadMessageText(items[1]); got != tt.originalText {
				t.Fatalf("original input = %q, want %q", got, tt.originalText)
			}
		})
	}
}

func TestBuildParamsOpenAICodexAddsJSONInstructionWhenOnlyInstructionsContainJSON(t *testing.T) {
	req := &chat.Request{
		Model:    "gpt-5.6-sol",
		Messages: []chat.Message{chat.User("Hi")},
		Options: chat.Options{OpenAI: structs.JSONMap{
			"instructions":    "Return valid JSON.",
			"response_format": "json_object",
		}},
	}

	params, err := buildParams(req, "", true)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	if got := params.Instructions.Value; got != "Return valid JSON." {
		t.Fatalf("instructions = %q, want original instructions", got)
	}
	items := responsePayloadMessages(t, params)
	if len(items) != 2 {
		t.Fatalf("input items = %d, want 2", len(items))
	}
	if items[0]["role"] != "user" {
		t.Fatalf("first item is not a user message: %#v", items[0])
	}
	if got := responsePayloadMessageText(items[0]); got != "Return the response as JSON." {
		t.Fatalf("JSON instruction = %q", got)
	}
	content, ok := items[0]["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("JSON instruction content = %#v, want one input_text part", items[0]["content"])
	}
	part, ok := content[0].(map[string]any)
	if !ok || part["type"] != "input_text" {
		t.Fatalf("JSON instruction part = %#v, want input_text", content[0])
	}
	if got := responsePayloadMessageText(items[1]); got != "Hi" {
		t.Fatalf("original input = %q, want Hi", got)
	}
}

func TestBuildParamsOpenAICodexAddsJSONInstructionWhenOnlyToolOutputContainsJSON(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.6-sol",
		Options: chat.Options{OpenAI: structs.JSONMap{
			"input": []any{
				map[string]any{
					"type": "message",
					"role": "user",
					"content": []any{
						map[string]any{
							"type": "input_text",
							"text": "Find the relevant issue and fix.",
						},
					},
				},
				map[string]any{
					"type":    "function_call_output",
					"call_id": "call_1",
					"output":  "Example source code: c.JSON(200, payload)",
				},
			},
			"response_format": "json_object",
		}},
	}

	params, err := buildParams(req, "", true)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	items := responsePayloadMessages(t, params)
	if len(items) != 3 {
		t.Fatalf("input items = %d, want 3", len(items))
	}
	if items[0]["role"] != "user" {
		t.Fatalf("first item is not a user message: %#v", items[0])
	}
	if got := responsePayloadMessageText(items[0]); got != "Return the response as JSON." {
		t.Fatalf("JSON instruction = %q", got)
	}
	if got := items[2]["type"]; got != "function_call_output" {
		t.Fatalf("last item type = %v, want function_call_output", got)
	}
}

func TestBuildParamsOpenAICodexDoesNotDuplicateJSONInstruction(t *testing.T) {
	tests := []struct {
		name       string
		options    structs.JSONMap
		message    string
		formatType string
	}{
		{
			name: "JSON in message",
			options: structs.JSONMap{
				"response_format": "json_object",
			},
			message:    "Return JSON only.",
			formatType: "json_object",
		},
		{
			name: "JSON schema",
			options: structs.JSONMap{
				"response_format": map[string]any{
					"type": "json_schema",
					"json_schema": map[string]any{
						"name":   "files",
						"strict": true,
						"schema": map[string]any{
							"type":                 "object",
							"properties":           map[string]any{},
							"additionalProperties": false,
						},
					},
				},
			},
			message:    "list changed files",
			formatType: "json_schema",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &chat.Request{
				Model:    "gpt-5.3-codex",
				Messages: []chat.Message{chat.User(tt.message)},
				Options:  chat.Options{OpenAI: tt.options},
			}
			params, err := buildParams(req, "", true)
			if err != nil {
				t.Fatalf("buildParams: %v", err)
			}
			if got := responsePayloadFormatType(t, params); got != tt.formatType {
				t.Fatalf("response format = %q, want %q", got, tt.formatType)
			}
			items := responsePayloadMessages(t, params)
			if got := len(items); got != 1 {
				t.Fatalf("input items = %d, want 1", got)
			}
			if got := responsePayloadMessageText(items[0]); got != tt.message {
				t.Fatalf("input text = %q, want %q", got, tt.message)
			}
		})
	}
}

func marshalResponseParams(t *testing.T, params responses.ResponseNewParams) map[string]any {
	t.Helper()
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	return payload
}

func responsePayloadMessages(t *testing.T, params responses.ResponseNewParams) []map[string]any {
	t.Helper()
	payload := marshalResponseParams(t, params)
	rawItems, ok := payload["input"].([]any)
	if !ok {
		t.Fatalf("response input is not an array: %#v", payload["input"])
	}
	items := make([]map[string]any, 0, len(rawItems))
	for i, rawItem := range rawItems {
		item, ok := rawItem.(map[string]any)
		if !ok {
			t.Fatalf("response input[%d] is not an object: %#v", i, rawItem)
		}
		items = append(items, item)
	}
	return items
}

func responsePayloadFormatType(t *testing.T, params responses.ResponseNewParams) string {
	t.Helper()
	payload := marshalResponseParams(t, params)
	textConfig, _ := payload["text"].(map[string]any)
	format, _ := textConfig["format"].(map[string]any)
	formatType, _ := format["type"].(string)
	return formatType
}

func responsePayloadMessageText(item map[string]any) string {
	if content, ok := item["content"].(string); ok {
		return content
	}
	content, _ := item["content"].([]any)
	var out strings.Builder
	for _, rawPart := range content {
		if part, ok := rawPart.(map[string]any); ok {
			if text, ok := part["text"].(string); ok {
				out.WriteString(text)
			}
		}
	}
	return out.String()
}
