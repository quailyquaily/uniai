package openairesp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/openai/openai-go/v3/responses"
	"github.com/quailyquaily/uniai/chat"
)

func TestBuildParamsMapsResponsesRequest(t *testing.T) {
	maxTokens := 256
	req := &chat.Request{
		Model: "gpt-5.4",
		Messages: []chat.Message{
			chat.UserParts(
				chat.TextPart("describe this"),
				chat.ImageBase64Part("image/png", "QUJD"),
			),
		},
		Options: chat.Options{
			MaxTokens: &maxTokens,
			ReasoningEffort: func() *chat.ReasoningEffort {
				v := chat.ReasoningEffortHigh
				return &v
			}(),
			ReasoningDetails: true,
			OpenAI: structs.JSONMap{
				"previous_response_id": "resp_prev",
				"parallel_tool_calls":  true,
				"verbosity":            "high",
				"response_format":      "json_object",
			},
		},
		Tools: []chat.Tool{
			chat.FunctionTool("get_weather", "desc", []byte(`{"type":"object","properties":{"city":{"type":"string"}}}`)),
		},
		ToolChoice: func() *chat.ToolChoice {
			c := chat.ToolChoiceFunction("get_weather")
			return &c
		}(),
	}

	params, err := buildParams(req, "")
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	if string(params.Model) != "gpt-5.4" {
		t.Fatalf("unexpected model: %q", params.Model)
	}
	if !params.MaxOutputTokens.Valid() || params.MaxOutputTokens.Value != int64(maxTokens) {
		t.Fatalf("unexpected max_output_tokens: %#v", params.MaxOutputTokens)
	}
	if params.Reasoning.Effort != "high" {
		t.Fatalf("unexpected reasoning effort: %q", params.Reasoning.Effort)
	}
	if params.Reasoning.Summary != "auto" {
		t.Fatalf("unexpected reasoning summary: %q", params.Reasoning.Summary)
	}
	if !params.PreviousResponseID.Valid() || params.PreviousResponseID.Value != "resp_prev" {
		t.Fatalf("unexpected previous_response_id: %#v", params.PreviousResponseID)
	}
	if !params.ParallelToolCalls.Valid() || !params.ParallelToolCalls.Value {
		t.Fatalf("parallel_tool_calls not set")
	}
	if params.Text.Verbosity != responses.ResponseTextConfigVerbosityHigh {
		t.Fatalf("unexpected verbosity: %q", params.Text.Verbosity)
	}
	if got := params.Text.Format.GetType(); got == nil || *got != "json_object" {
		t.Fatalf("unexpected response format type: %#v", got)
	}
	if len(params.Tools) != 1 || params.Tools[0].OfFunction == nil {
		t.Fatalf("expected one function tool, got %#v", params.Tools)
	}
	if got, ok := params.Tools[0].OfFunction.Parameters["additionalProperties"].(bool); !ok || got {
		t.Fatalf("expected additionalProperties=false, got %#v", params.Tools[0].OfFunction.Parameters["additionalProperties"])
	}
	if len(params.Input.OfInputItemList) != 1 || params.Input.OfInputItemList[0].OfMessage == nil {
		t.Fatalf("expected one input message, got %#v", params.Input)
	}
	content := params.Input.OfInputItemList[0].OfMessage.Content.OfInputItemContentList
	if len(content) != 2 {
		t.Fatalf("expected 2 user content items, got %d", len(content))
	}
	if content[0].OfInputText == nil || content[0].OfInputText.Text != "describe this" {
		t.Fatalf("unexpected first content item: %#v", content[0])
	}
	if content[1].OfInputImage == nil {
		t.Fatalf("expected input_image content item")
	}
	if got := content[1].OfInputImage.ImageURL.Value; got != "data:image/png;base64,QUJD" {
		t.Fatalf("unexpected image url: %q", got)
	}
}

func TestBuildParamsRejectsInputConflict(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.4",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			OpenAI: structs.JSONMap{
				"input": []map[string]any{
					{"role": "user", "content": "raw"},
				},
			},
		},
	}

	_, err := buildParams(req, "")
	if err == nil || !strings.Contains(err.Error(), "openai.input") {
		t.Fatalf("expected input conflict error, got %v", err)
	}
}

func TestBuildParamsAllowsRawInputWithoutMessages(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.4",
		Options: chat.Options{
			OpenAI: structs.JSONMap{
				"input": "raw input",
			},
		},
	}

	params, err := buildParams(req, "")
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	if !params.Input.OfString.Valid() || params.Input.OfString.Value != "raw input" {
		t.Fatalf("expected raw string input, got %#v", params.Input)
	}
}

func TestBuildParamsRejectsUnsupportedSharedOptions(t *testing.T) {
	cases := []struct {
		name  string
		apply func(*chat.Request)
		want  string
	}{
		{
			name: "stop",
			apply: func(req *chat.Request) {
				req.Options.Stop = []string{"END"}
			},
			want: "stop sequences",
		},
		{
			name: "presence penalty",
			apply: func(req *chat.Request) {
				v := 0.1
				req.Options.PresencePenalty = &v
			},
			want: "presence penalty",
		},
		{
			name: "frequency penalty",
			apply: func(req *chat.Request) {
				v := 0.2
				req.Options.FrequencyPenalty = &v
			},
			want: "frequency penalty",
		},
		{
			name: "reasoning budget",
			apply: func(req *chat.Request) {
				v := 2048
				req.Options.ReasoningBudget = &v
			},
			want: "reasoning budget tokens",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &chat.Request{
				Model: "gpt-5.4",
				Messages: []chat.Message{
					chat.User("hello"),
				},
			}
			tc.apply(req)

			_, err := buildParams(req, "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestBuildParamsRejectsUnsupportedOpenAIOptionKeys(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.4",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			OpenAI: structs.JSONMap{
				"unsupported_key": true,
			},
		},
	}

	_, err := buildParams(req, "")
	if err == nil || !strings.Contains(err.Error(), "unsupported_key") {
		t.Fatalf("expected unsupported openai option error, got %v", err)
	}
}

func TestBuildParamsRejectsResponsesConflicts(t *testing.T) {
	cases := []struct {
		name string
		req  *chat.Request
		want string
	}{
		{
			name: "reasoning raw plus compat",
			req: &chat.Request{
				Model: "gpt-5.4",
				Messages: []chat.Message{
					chat.User("hello"),
				},
				Options: chat.Options{
					ReasoningEffort: func() *chat.ReasoningEffort {
						v := chat.ReasoningEffortHigh
						return &v
					}(),
					OpenAI: structs.JSONMap{
						"reasoning": map[string]any{"effort": "high"},
					},
				},
			},
			want: "openai.reasoning",
		},
		{
			name: "tool choice raw plus compat",
			req: &chat.Request{
				Model: "gpt-5.4",
				Messages: []chat.Message{
					chat.User("hello"),
				},
				Options: chat.Options{
					OpenAI: structs.JSONMap{
						"tool_choice": "auto",
					},
				},
				ToolChoice: func() *chat.ToolChoice {
					v := chat.ToolChoiceRequired()
					return &v
				}(),
			},
			want: "openai.tool_choice",
		},
		{
			name: "previous response id plus conversation",
			req: &chat.Request{
				Model: "gpt-5.4",
				Messages: []chat.Message{
					chat.User("hello"),
				},
				Options: chat.Options{
					OpenAI: structs.JSONMap{
						"previous_response_id": "resp_prev",
						"conversation":         map[string]any{"id": "conv_123"},
					},
				},
			},
			want: "openai.previous_response_id",
		},
		{
			name: "text plus verbosity shortcut",
			req: &chat.Request{
				Model: "gpt-5.4",
				Messages: []chat.Message{
					chat.User("hello"),
				},
				Options: chat.Options{
					OpenAI: structs.JSONMap{
						"text":      map[string]any{"format": map[string]any{"type": "text"}},
						"verbosity": "high",
					},
				},
			},
			want: "openai.text",
		},
		{
			name: "raw user plus compat user",
			req: &chat.Request{
				Model: "gpt-5.4",
				Messages: []chat.Message{
					chat.User("hello"),
				},
				Options: chat.Options{
					User: func() *string {
						v := "compat-user"
						return &v
					}(),
					OpenAI: structs.JSONMap{
						"user": "raw-user",
					},
				},
			},
			want: "openai.user",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildParams(tc.req, "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestBuildParamsMergesRawAndCompatTools(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.4",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			OpenAI: structs.JSONMap{
				"tools": []map[string]any{
					{
						"type":       "function",
						"name":       "raw_tool",
						"parameters": map[string]any{"type": "object"},
						"strict":     true,
					},
				},
			},
		},
		Tools: []chat.Tool{
			chat.FunctionTool("compat_tool", "desc", []byte(`{"type":"object"}`)),
		},
	}

	params, err := buildParams(req, "")
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	if len(params.Tools) != 2 {
		t.Fatalf("expected 2 merged tools, got %d", len(params.Tools))
	}
	if params.Tools[0].OfFunction == nil || params.Tools[0].OfFunction.Name != "raw_tool" {
		t.Fatalf("unexpected raw tool: %#v", params.Tools[0])
	}
	if params.Tools[1].OfFunction == nil || params.Tools[1].OfFunction.Name != "compat_tool" {
		t.Fatalf("unexpected compat tool: %#v", params.Tools[1])
	}
	if got, ok := params.Tools[1].OfFunction.Parameters["additionalProperties"].(bool); !ok || got {
		t.Fatalf("expected compat tool additionalProperties=false, got %#v", params.Tools[1].OfFunction.Parameters["additionalProperties"])
	}
}

func TestBuildParamsAddsAdditionalPropertiesFalseRecursively(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.4",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Tools: []chat.Tool{
			chat.FunctionTool("compat_tool", "desc", []byte(`{
				"type":"object",
				"properties":{
					"payload":{
						"type":"object",
						"properties":{
							"city":{"type":"string"}
						}
					}
				}
			}`)),
		},
	}

	params, err := buildParams(req, "")
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	tool := params.Tools[0].OfFunction
	if tool == nil {
		t.Fatalf("expected compat function tool, got %#v", params.Tools)
	}
	payload, ok := tool.Parameters["properties"].(map[string]any)["payload"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested payload object, got %#v", tool.Parameters)
	}
	if got, ok := payload["additionalProperties"].(bool); !ok || got {
		t.Fatalf("expected nested additionalProperties=false, got %#v", payload["additionalProperties"])
	}
}

func TestBuildParamsRejectsUnsupportedMessageShapes(t *testing.T) {
	cases := []struct {
		name string
		msg  chat.Message
		want string
	}{
		{
			name: "message name",
			msg: chat.Message{
				Role:    chat.RoleUser,
				Content: "hello",
				Name:    "named-user",
			},
			want: "message names",
		},
		{
			name: "image base64 missing mime type",
			msg: chat.UserParts(chat.Part{
				Type:       chat.PartTypeImageBase64,
				DataBase64: "QUJD",
			}),
			want: "requires mime_type",
		},
		{
			name: "tool message missing tool call id",
			msg: chat.Message{
				Role:    chat.RoleTool,
				Content: "result",
			},
			want: "tool_call_id is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &chat.Request{
				Model: "gpt-5.4",
				Messages: []chat.Message{
					tc.msg,
				},
			}

			_, err := buildParams(req, "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestToResultParsesResponsesOutput(t *testing.T) {
	resp := mustDecodeResponse(t, map[string]any{
		"id":                  "resp_123",
		"model":               "gpt-5.4",
		"object":              "response",
		"parallel_tool_calls": true,
		"temperature":         1,
		"tool_choice":         "auto",
		"tools":               []any{},
		"top_p":               1,
		"status":              "completed",
		"output": []any{
			map[string]any{
				"id":     "msg_1",
				"type":   "message",
				"role":   "assistant",
				"status": "completed",
				"content": []any{
					map[string]any{
						"type":        "output_text",
						"text":        "hello",
						"annotations": []any{},
					},
				},
			},
			map[string]any{
				"id":        "fc_1",
				"type":      "function_call",
				"status":    "completed",
				"call_id":   "call_1",
				"name":      "get_weather",
				"arguments": `{"city":"Tokyo"}`,
			},
			map[string]any{
				"id":                "rs_1",
				"type":              "reasoning",
				"status":            "completed",
				"summary":           []any{map[string]any{"type": "summary_text", "text": "summary"}},
				"content":           []any{map[string]any{"type": "reasoning_text", "text": "thought"}},
				"encrypted_content": "enc",
			},
		},
		"usage": map[string]any{
			"input_tokens":         10,
			"input_tokens_details": map[string]any{},
			"output_tokens":        5,
			"output_tokens_details": map[string]any{
				"reasoning_tokens": 0,
			},
			"total_tokens": 15,
		},
		"text": map[string]any{
			"format": map[string]any{"type": "text"},
		},
	})

	result := toResult(resp)
	if result.ID != "resp_123" {
		t.Fatalf("unexpected result id: %q", result.ID)
	}
	if result.Text != "hello" {
		t.Fatalf("unexpected text: %q", result.Text)
	}
	if len(result.Parts) != 1 || result.Parts[0].Text != "hello" {
		t.Fatalf("unexpected parts: %#v", result.Parts)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].ID != "call_1" {
		t.Fatalf("unexpected tool calls: %#v", result.ToolCalls)
	}
	if result.Reasoning == nil || len(result.Reasoning.Summary) != 1 || result.Reasoning.Summary[0] != "summary" {
		t.Fatalf("unexpected reasoning summary: %#v", result.Reasoning)
	}
	if len(result.Reasoning.Blocks) != 2 {
		t.Fatalf("unexpected reasoning blocks: %#v", result.Reasoning.Blocks)
	}
	if result.Usage.TotalTokens != 15 {
		t.Fatalf("unexpected usage: %#v", result.Usage)
	}
}

func TestResponseStatusError(t *testing.T) {
	cases := []struct {
		name string
		resp *responses.Response
		want string
	}{
		{
			name: "failed",
			resp: mustDecodeResponse(t, map[string]any{
				"id":     "resp_fail",
				"object": "response",
				"model":  "gpt-5.4",
				"status": "failed",
				"error": map[string]any{
					"message": "boom",
				},
			}),
			want: "boom",
		},
		{
			name: "incomplete",
			resp: mustDecodeResponse(t, map[string]any{
				"id":     "resp_incomplete",
				"object": "response",
				"model":  "gpt-5.4",
				"status": "incomplete",
				"incomplete_details": map[string]any{
					"reason": "max_output_tokens",
				},
			}),
			want: "max_output_tokens",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := responseStatusError(tc.resp)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestProcessStreamEventParsesDeltasAndCompletion(t *testing.T) {
	events := []responses.ResponseStreamEventUnion{
		mustDecodeStreamEvent(t, map[string]any{
			"type":            "response.output_item.added",
			"output_index":    0,
			"sequence_number": 1,
			"item": map[string]any{
				"id":        "fc_1",
				"type":      "function_call",
				"status":    "in_progress",
				"call_id":   "call_1",
				"name":      "get_weather",
				"arguments": "",
			},
		}),
		mustDecodeStreamEvent(t, map[string]any{
			"type":            "response.function_call_arguments.delta",
			"output_index":    0,
			"sequence_number": 2,
			"item_id":         "fc_1",
			"delta":           `{"city":"To`,
		}),
		mustDecodeStreamEvent(t, map[string]any{
			"type":            "response.function_call_arguments.done",
			"output_index":    0,
			"sequence_number": 3,
			"item_id":         "fc_1",
			"name":            "get_weather",
			"arguments":       `{"city":"Tokyo"}`,
		}),
		mustDecodeStreamEvent(t, map[string]any{
			"type":            "response.output_text.delta",
			"output_index":    1,
			"content_index":   0,
			"sequence_number": 4,
			"item_id":         "msg_1",
			"delta":           "Hello",
			"logprobs":        []any{},
		}),
		mustDecodeStreamEvent(t, map[string]any{
			"type":            "response.completed",
			"sequence_number": 5,
			"response": map[string]any{
				"id":                  "resp_456",
				"model":               "gpt-5.4",
				"object":              "response",
				"parallel_tool_calls": true,
				"temperature":         1,
				"tool_choice":         "auto",
				"tools":               []any{},
				"top_p":               1,
				"status":              "completed",
				"output": []any{
					map[string]any{
						"id":     "msg_1",
						"type":   "message",
						"role":   "assistant",
						"status": "completed",
						"content": []any{
							map[string]any{
								"type":        "output_text",
								"text":        "Hello",
								"annotations": []any{},
							},
						},
					},
				},
				"usage": map[string]any{
					"input_tokens":         8,
					"input_tokens_details": map[string]any{},
					"output_tokens":        3,
					"output_tokens_details": map[string]any{
						"reasoning_tokens": 0,
					},
					"total_tokens": 11,
				},
				"text": map[string]any{
					"format": map[string]any{"type": "text"},
				},
			},
		}),
	}

	state := &responseStreamState{toolCalls: map[int]streamToolCallState{}}
	var textDeltas []string
	var toolDeltas []chat.ToolCallDelta
	for _, ev := range events {
		err := processStreamEvent(ev, state, func(event chat.StreamEvent) error {
			if event.Delta != "" {
				textDeltas = append(textDeltas, event.Delta)
			}
			if event.ToolCallDelta != nil {
				toolDeltas = append(toolDeltas, *event.ToolCallDelta)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("processStreamEvent: %v", err)
		}
	}

	if strings.Join(textDeltas, "") != "Hello" {
		t.Fatalf("unexpected text deltas: %#v", textDeltas)
	}
	if len(toolDeltas) != 2 {
		t.Fatalf("unexpected tool deltas: %#v", toolDeltas)
	}
	if toolDeltas[0].ID != "call_1" || toolDeltas[0].Name != "get_weather" || toolDeltas[0].ArgsChunk == "" {
		t.Fatalf("unexpected first tool delta: %#v", toolDeltas[0])
	}
	if toolDeltas[1].ID != "call_1" || toolDeltas[1].Name != "get_weather" {
		t.Fatalf("unexpected done tool delta: %#v", toolDeltas[1])
	}
	if state.completed == nil || state.completed.ID != "resp_456" {
		t.Fatalf("expected completed response, got %#v", state.completed)
	}
}

func mustDecodeResponse(t *testing.T, payload map[string]any) *responses.Response {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal response payload: %v", err)
	}
	var out responses.Response
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return &out
}

func mustDecodeStreamEvent(t *testing.T, payload map[string]any) responses.ResponseStreamEventUnion {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal stream payload: %v", err)
	}
	var out responses.ResponseStreamEventUnion
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal stream event: %v", err)
	}
	return out
}
