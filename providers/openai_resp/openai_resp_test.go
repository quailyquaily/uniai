package openairesp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

	params, err := buildParams(req, "", false)
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
	if !params.Tools[0].OfFunction.Strict.Valid() || params.Tools[0].OfFunction.Strict.Value {
		t.Fatalf("expected compat tool strict=false by default, got %#v", params.Tools[0].OfFunction.Strict)
	}
	if _, ok := params.Tools[0].OfFunction.Parameters["additionalProperties"]; ok {
		t.Fatalf("expected default compat schema to preserve additionalProperties, got %#v", params.Tools[0].OfFunction.Parameters["additionalProperties"])
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

func TestChatAggregatesEventStreamOnNonStreamingRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeOpenAIResponseSSE(t, w, map[string]any{
			"type":            "response.completed",
			"sequence_number": 1,
			"response": map[string]any{
				"id":                  "resp_123",
				"model":               "gpt-5.4",
				"object":              "response",
				"parallel_tool_calls": true,
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
				},
				"usage": map[string]any{
					"input_tokens":         2,
					"input_tokens_details": map[string]any{},
					"output_tokens":        1,
					"output_tokens_details": map[string]any{
						"reasoning_tokens": 0,
					},
					"total_tokens": 3,
				},
				"text": map[string]any{
					"format": map[string]any{"type": "text"},
				},
			},
		})
	}))
	defer server.Close()

	p, err := New(Config{
		APIKey:       "test-key",
		BaseURL:      server.URL + "/v1",
		DefaultModel: "gpt-5.4",
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	resp, err := p.Chat(context.Background(), &chat.Request{
		Messages: []chat.Message{
			chat.User("hello"),
		},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if resp.Text != "hello" {
		t.Fatalf("unexpected text: %q", resp.Text)
	}
	if resp.Usage.TotalTokens != 3 {
		t.Fatalf("unexpected usage: %#v", resp.Usage)
	}
}

func TestChatRetriesEmptyEventStreamOnNonStreamingRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		requests++
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		if requests == 1 {
			if strings.Contains(string(data), `"stream":true`) {
				t.Fatalf("first request should be non-streaming, body: %s", string(data))
			}
			return
		}
		if requests != 2 {
			t.Fatalf("unexpected request count: %d", requests)
		}
		if !strings.Contains(string(data), `"stream":true`) {
			t.Fatalf("retry should request a real stream, body: %s", string(data))
		}
		writeOpenAIResponseSSE(t, w, map[string]any{
			"type":            "response.completed",
			"sequence_number": 1,
			"response": map[string]any{
				"id":                  "resp_retry",
				"model":               "gpt-5.4",
				"object":              "response",
				"parallel_tool_calls": true,
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
				},
				"usage": map[string]any{
					"input_tokens":         2,
					"input_tokens_details": map[string]any{},
					"output_tokens":        1,
					"output_tokens_details": map[string]any{
						"reasoning_tokens": 0,
					},
					"total_tokens": 3,
				},
				"text": map[string]any{
					"format": map[string]any{"type": "text"},
				},
			},
		})
	}))
	defer server.Close()

	p, err := New(Config{
		APIKey:       "test-key",
		BaseURL:      server.URL + "/v1",
		DefaultModel: "gpt-5.4",
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	resp, err := p.Chat(context.Background(), &chat.Request{
		Messages: []chat.Message{
			chat.User("hello"),
		},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
	if resp.Text != "hello" {
		t.Fatalf("unexpected text: %q", resp.Text)
	}
}

func TestBuildParamsDropsGPT5ReasoningSamplingParams(t *testing.T) {
	temp := 0.2
	topP := 0.9
	req := &chat.Request{
		Model: "gpt-5.4",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			Temperature: &temp,
			TopP:        &topP,
			ReasoningEffort: func() *chat.ReasoningEffort {
				v := chat.ReasoningEffortHigh
				return &v
			}(),
			OpenAI: structs.JSONMap{
				"top_logprobs": 3,
			},
		},
	}

	params, err := buildParams(req, "", false)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	if params.Temperature.Valid() || params.TopP.Valid() || params.TopLogprobs.Valid() {
		t.Fatalf("expected GPT-5 reasoning sampling params to be omitted, got %#v", params)
	}
	if params.Reasoning.Effort != "high" {
		t.Fatalf("unexpected reasoning effort: %q", params.Reasoning.Effort)
	}
}

func TestBuildParamsKeepsGPT54SamplingWithReasoningNone(t *testing.T) {
	temp := 0.2
	topP := 0.9
	req := &chat.Request{
		Model: "gpt-5.4",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			Temperature: &temp,
			TopP:        &topP,
			ReasoningEffort: func() *chat.ReasoningEffort {
				v := chat.ReasoningEffortNone
				return &v
			}(),
		},
	}

	params, err := buildParams(req, "", false)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	if !params.Temperature.Valid() || params.Temperature.Value != temp {
		t.Fatalf("expected temperature to be preserved, got %#v", params.Temperature)
	}
	if !params.TopP.Valid() || params.TopP.Value != topP {
		t.Fatalf("expected top_p to be preserved, got %#v", params.TopP)
	}
}

func TestBuildParamsDropsGPT55SamplingParams(t *testing.T) {
	temp := 0.2
	req := &chat.Request{
		Model: "gpt-5.5",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			Temperature: &temp,
		},
	}

	params, err := buildParams(req, "", false)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	if params.Temperature.Valid() {
		t.Fatalf("expected GPT-5.5 temperature to be omitted, got %#v", params.Temperature)
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

	_, err := buildParams(req, "", false)
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

	params, err := buildParams(req, "", false)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	if !params.Input.OfString.Valid() || params.Input.OfString.Value != "raw input" {
		t.Fatalf("expected raw string input, got %#v", params.Input)
	}
}

func TestBuildParamsMapsPromptCacheRetention(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.4",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			OpenAI: structs.JSONMap{
				"prompt_cache_retention": "24h",
			},
		},
	}

	params, err := buildParams(req, "", false)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if !strings.Contains(string(data), `"prompt_cache_retention":"24h"`) {
		t.Fatalf("expected prompt_cache_retention in payload, got %s", string(data))
	}
}

func TestBuildParamsValidatesPromptCacheKeyLength(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{name: "64 ASCII characters", key: strings.Repeat("a", 64)},
		{name: "65 ASCII characters", key: strings.Repeat("a", 65), wantErr: true},
		{name: "64 Unicode characters", key: strings.Repeat("界", 64)},
		{name: "65 Unicode characters", key: strings.Repeat("界", 65), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &chat.Request{
				Model: "gpt-5.4",
				Messages: []chat.Message{
					chat.User("hello"),
				},
				Options: chat.Options{
					OpenAI: structs.JSONMap{
						"prompt_cache_key": tt.key,
					},
				},
			}

			_, err := buildParams(req, "", false)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected prompt_cache_key length error")
			}
			if !strings.Contains(err.Error(), "prompt_cache_key must not exceed 64 characters") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestBuildParamsForcesGPT55PromptCacheRetentionTo24h(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.5",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			OpenAI: structs.JSONMap{
				"prompt_cache_key":       "abc",
				"prompt_cache_retention": "in_memory",
			},
		},
	}

	params, err := buildParams(req, "", false)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if !strings.Contains(string(data), `"prompt_cache_retention":"24h"`) {
		t.Fatalf("expected prompt_cache_retention=24h in payload, got %s", string(data))
	}
	if strings.Contains(string(data), "in_memory") {
		t.Fatalf("expected in_memory to be replaced, got %s", string(data))
	}
}

func TestBuildParamsMapsGPT56PromptCacheOptions(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.6",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			OpenAI: structs.JSONMap{
				"prompt_cache_options": map[string]any{
					"mode": "explicit",
					"ttl":  "30m",
				},
			},
		},
	}

	params, err := buildParams(req, "", false)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	payload := string(data)
	if !strings.Contains(payload, `"prompt_cache_options"`) ||
		!strings.Contains(payload, `"mode":"explicit"`) ||
		!strings.Contains(payload, `"ttl":"30m"`) {
		t.Fatalf("expected GPT-5.6 prompt_cache_options, got %s", payload)
	}
}

func TestBuildParamsMapsGPT56SystemPromptCacheBreakpoint(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.6",
		Messages: []chat.Message{
			chat.SystemParts(chat.WithPartCacheControl(
				chat.TextPart("stable system prompt"),
				chat.CacheControl{},
			)),
			chat.User("dynamic user input"),
		},
		Options: chat.Options{
			OpenAI: structs.JSONMap{
				"prompt_cache_key": "cache-key",
				"prompt_cache_options": map[string]any{
					"mode": "explicit",
					"ttl":  "30m",
				},
			},
		},
	}

	params, err := buildParams(req, "", false)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	payload := string(data)
	if !strings.Contains(payload, `"content":[{"text":"stable system prompt","prompt_cache_breakpoint":{"mode":"explicit"},"type":"input_text"}]`) {
		t.Fatalf("expected system prompt cache breakpoint, got %s", payload)
	}
	if strings.Count(payload, `"prompt_cache_breakpoint"`) != 1 {
		t.Fatalf("expected exactly one prompt cache breakpoint, got %s", payload)
	}
}

func TestBuildParamsMapsGPT56LunaSystemPromptCacheBreakpoint(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.6-luna",
		Messages: []chat.Message{
			chat.SystemParts(chat.WithPartCacheControl(
				chat.TextPart("stable system prompt"),
				chat.CacheControl{},
			)),
			chat.User("dynamic user input"),
		},
	}

	params, err := buildParams(req, "", false)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if !strings.Contains(string(data), `"prompt_cache_breakpoint":{"mode":"explicit"}`) {
		t.Fatalf("expected Luna prompt cache breakpoint, got %s", string(data))
	}
}

func TestBuildParamsMapsGPT56LegacyPromptCacheRetention(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.6-sol",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			OpenAI: structs.JSONMap{
				"prompt_cache_retention": "24h",
			},
		},
	}

	params, err := buildParams(req, "", false)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	payload := string(data)
	if strings.Contains(payload, `"prompt_cache_retention"`) {
		t.Fatalf("expected legacy prompt_cache_retention to be removed, got %s", payload)
	}
	if !strings.Contains(payload, `"prompt_cache_options"`) || !strings.Contains(payload, `"ttl":"30m"`) {
		t.Fatalf("expected legacy retention to map to prompt_cache_options.ttl=30m, got %s", payload)
	}
}

func TestBuildParamsPrefersGPT56PromptCacheOptionsOverLegacyRetention(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.6-luna",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			OpenAI: structs.JSONMap{
				"prompt_cache_retention": "24h",
				"prompt_cache_options": map[string]any{
					"mode": "explicit",
				},
			},
		},
	}

	params, err := buildParams(req, "", false)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	payload := string(data)
	if strings.Contains(payload, `"prompt_cache_retention"`) {
		t.Fatalf("expected legacy prompt_cache_retention to be removed, got %s", payload)
	}
	if !strings.Contains(payload, `"mode":"explicit"`) {
		t.Fatalf("expected explicit prompt_cache_options to win, got %s", payload)
	}
}

func TestBuildParamsMapsGPT56ReasoningOptions(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.6",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			OpenAI: structs.JSONMap{
				"reasoning": map[string]any{
					"effort":  "max",
					"mode":    "pro",
					"context": "all_turns",
				},
			},
		},
	}

	params, err := buildParams(req, "", false)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	payload := string(data)
	for _, want := range []string{`"effort":"max"`, `"mode":"pro"`, `"context":"all_turns"`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("expected %s in GPT-5.6 reasoning options, got %s", want, payload)
		}
	}
}

func TestBuildParamsMapsGPT56LunaReasoningDetails(t *testing.T) {
	effort := chat.ReasoningEffortMedium
	req := &chat.Request{
		Model:    "gpt-5.6-luna",
		Messages: []chat.Message{chat.User("hello")},
		Options: chat.Options{
			ReasoningEffort:  &effort,
			ReasoningDetails: true,
		},
	}

	params, err := buildParams(req, "", false)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	if string(params.Model) != "gpt-5.6-luna" {
		t.Fatalf("unexpected model: %q", params.Model)
	}
	if params.Reasoning.Effort != "medium" || params.Reasoning.Summary != "auto" {
		t.Fatalf("unexpected Luna reasoning config: %#v", params.Reasoning)
	}
}

func TestBuildParamsMapsGPT56RawPromptCacheBreakpoint(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.6",
		Options: chat.Options{
			OpenAI: structs.JSONMap{
				"input": []map[string]any{
					{
						"role": "user",
						"content": []map[string]any{
							{
								"type": "input_text",
								"text": "stable prompt prefix",
								"prompt_cache_breakpoint": map[string]any{
									"mode": "explicit",
								},
							},
						},
					},
				},
				"prompt_cache_options": map[string]any{
					"mode": "explicit",
				},
			},
		},
	}

	params, err := buildParams(req, "", false)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if !strings.Contains(string(data), `"prompt_cache_breakpoint":{"mode":"explicit"}`) {
		t.Fatalf("expected raw prompt cache breakpoint, got %s", string(data))
	}
}

func TestBuildParamsMapsGPT56LunaRawPromptCacheBreakpoint(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.6-luna",
		Options: chat.Options{
			OpenAI: structs.JSONMap{
				"input": []map[string]any{
					{
						"role": "user",
						"content": []map[string]any{
							{
								"type": "input_text",
								"text": "stable prompt prefix",
								"prompt_cache_breakpoint": map[string]any{
									"mode": "explicit",
								},
							},
						},
					},
				},
			},
		},
	}

	params, err := buildParams(req, "", false)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if !strings.Contains(string(data), `"prompt_cache_breakpoint":{"mode":"explicit"}`) {
		t.Fatalf("expected Luna raw prompt cache breakpoint, got %s", string(data))
	}
}

func TestBuildParamsRejectsInvalidGPT56RawPromptCacheBreakpoint(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.6",
		Options: chat.Options{
			OpenAI: structs.JSONMap{
				"input": []map[string]any{
					{
						"role": "user",
						"content": []map[string]any{
							{
								"type": "input_text",
								"text": "stable prompt prefix",
								"prompt_cache_breakpoint": map[string]any{
									"mode": "implicit",
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := buildParams(req, "", false)
	if err == nil || !strings.Contains(err.Error(), "prompt_cache_breakpoint") || !strings.Contains(err.Error(), "explicit") {
		t.Fatalf("expected invalid prompt cache breakpoint error, got %v", err)
	}
}

func TestBuildParamsRejectsGPT56MinimalReasoningEffort(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.6",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			ReasoningEffort: func() *chat.ReasoningEffort {
				v := chat.ReasoningEffortMinimal
				return &v
			}(),
		},
	}

	_, err := buildParams(req, "", false)
	if err == nil || !strings.Contains(err.Error(), "minimal") || !strings.Contains(err.Error(), "gpt-5.6") {
		t.Fatalf("expected GPT-5.6 minimal reasoning effort error, got %v", err)
	}
}

func TestBuildParamsRejectsInvalidGPT56ReasoningOptions(t *testing.T) {
	cases := []struct {
		name  string
		field string
		value string
	}{
		{name: "mode", field: "mode", value: "automatic"},
		{name: "context", field: "context", value: "conversation"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &chat.Request{
				Model:    "gpt-5.6",
				Messages: []chat.Message{chat.User("hello")},
				Options: chat.Options{
					OpenAI: structs.JSONMap{
						"reasoning": map[string]any{tc.field: tc.value},
					},
				},
			}

			_, err := buildParams(req, "", false)
			if err == nil || !strings.Contains(err.Error(), tc.field) || !strings.Contains(err.Error(), tc.value) {
				t.Fatalf("expected invalid reasoning %s error, got %v", tc.field, err)
			}
		})
	}
}

func TestBuildParamsRejectsInvalidGPT56PromptCacheOptions(t *testing.T) {
	cases := []struct {
		name    string
		options map[string]any
		want    string
	}{
		{
			name: "ttl",
			options: map[string]any{
				"ttl": "24h",
			},
			want: "ttl",
		},
		{
			name: "mode",
			options: map[string]any{
				"mode": "automatic",
			},
			want: "mode",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &chat.Request{
				Model:    "gpt-5.6",
				Messages: []chat.Message{chat.User("hello")},
				Options: chat.Options{
					OpenAI: structs.JSONMap{
						"prompt_cache_options": tc.options,
					},
				},
			}

			_, err := buildParams(req, "", false)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected invalid %s error, got %v", tc.want, err)
			}
		})
	}
}

func TestBuildParamsRejectsExplicitCacheControl(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.4",
		Messages: []chat.Message{
			chat.UserParts(chat.WithPartCacheControl(chat.TextPart("hello"), chat.CacheTTL5m())),
		},
	}

	_, err := buildParams(req, "", false)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "explicit cache control") {
		t.Fatalf("unexpected error: %v", err)
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

			_, err := buildParams(req, "", false)
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

	_, err := buildParams(req, "", false)
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
			_, err := buildParams(tc.req, "", false)
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

	params, err := buildParams(req, "", false)
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
	if !params.Tools[1].OfFunction.Strict.Valid() || params.Tools[1].OfFunction.Strict.Value {
		t.Fatalf("expected compat tool strict=false by default, got %#v", params.Tools[1].OfFunction.Strict)
	}
	if _, ok := params.Tools[1].OfFunction.Parameters["additionalProperties"]; ok {
		t.Fatalf("expected compat tool schema to preserve additionalProperties by default, got %#v", params.Tools[1].OfFunction.Parameters["additionalProperties"])
	}
}

func TestBuildParamsNormalizesStrictSchemasRecursively(t *testing.T) {
	strict := true
	req := &chat.Request{
		Model: "gpt-5.4",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Tools: []chat.Tool{
			{
				Type: "function",
				Function: chat.ToolFunction{
					Name:        "compat_tool",
					Description: "desc",
					ParametersJSONSchema: []byte(`{
						"type":"object",
						"properties":{
							"payload":{
								"type":"object",
								"properties":{
									"city":{"type":"string"}
								}
							}
						}
					}`),
					Strict: &strict,
				},
			},
		},
	}

	params, err := buildParams(req, "", false)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	tool := params.Tools[0].OfFunction
	if tool == nil {
		t.Fatalf("expected compat function tool, got %#v", params.Tools)
	}
	if !tool.Strict.Valid() || !tool.Strict.Value {
		t.Fatalf("expected strict=true, got %#v", tool.Strict)
	}
	payload, ok := tool.Parameters["properties"].(map[string]any)["payload"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested payload object, got %#v", tool.Parameters)
	}
	if got, ok := payload["additionalProperties"].(bool); !ok || got {
		t.Fatalf("expected nested additionalProperties=false, got %#v", payload["additionalProperties"])
	}
}

func TestBuildParamsPreservesOptionalFieldsWhenStrictIsDefaultedOff(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-5.4",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Tools: []chat.Tool{
			chat.FunctionTool("bash", "desc", []byte(`{
				"type":"object",
				"properties":{
					"cmd":{"type":"string"},
					"cwd":{"type":"string"},
					"timeout_seconds":{"type":"number"}
				},
				"required":["cmd"]
			}`)),
		},
	}

	params, err := buildParams(req, "", false)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	tool := params.Tools[0].OfFunction
	if tool == nil {
		t.Fatalf("expected compat function tool, got %#v", params.Tools)
	}
	if !tool.Strict.Valid() || tool.Strict.Value {
		t.Fatalf("expected strict=false by default, got %#v", tool.Strict)
	}
	required, ok := tool.Parameters["required"].([]any)
	if !ok {
		t.Fatalf("expected required array, got %#v", tool.Parameters["required"])
	}
	if len(required) != 1 || required[0] != "cmd" {
		t.Fatalf("expected required to preserve only cmd, got %#v", required)
	}
	if _, ok := tool.Parameters["additionalProperties"]; ok {
		t.Fatalf("expected default schema to avoid strict normalization, got %#v", tool.Parameters["additionalProperties"])
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

			_, err := buildParams(req, "", false)
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
			"input_tokens": 10,
			"input_tokens_details": map[string]any{
				"cached_tokens":      6,
				"cache_write_tokens": 4,
			},
			"output_tokens": 5,
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
	if result.Usage.Cache.CachedInputTokens != 6 {
		t.Fatalf("unexpected cache usage: %#v", result.Usage.Cache)
	}
	if result.Usage.Cache.CacheCreationInputTokens != 4 {
		t.Fatalf("unexpected cache usage: %#v", result.Usage.Cache)
	}
}

func TestToResultIncludesSakanaOrchestrationTokens(t *testing.T) {
	resp := mustDecodeResponse(t, map[string]any{
		"id":                  "resp_sakana",
		"object":              "response",
		"model":               "fugu-ultra",
		"parallel_tool_calls": true,
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
		},
		"usage": map[string]any{
			"input_tokens": 120,
			"input_tokens_details": map[string]any{
				"cached_tokens":                     10,
				"orchestration_input_tokens":        20,
				"orchestration_input_cached_tokens": 5,
			},
			"output_tokens": 80,
			"output_tokens_details": map[string]any{
				"reasoning_tokens":            0,
				"orchestration_output_tokens": 30,
			},
			"total_tokens": 250,
		},
		"text": map[string]any{
			"format": map[string]any{"type": "text"},
		},
	})

	result := toResult(resp)
	if result.Usage.InputTokens != 140 {
		t.Fatalf("input tokens = %d, want 140", result.Usage.InputTokens)
	}
	if result.Usage.Cache.CachedInputTokens != 15 {
		t.Fatalf("cached input tokens = %d, want 15", result.Usage.Cache.CachedInputTokens)
	}
	if result.Usage.OutputTokens != 110 {
		t.Fatalf("output tokens = %d, want 110", result.Usage.OutputTokens)
	}
	if result.Usage.TotalTokens != 250 {
		t.Fatalf("total tokens = %d, want 250", result.Usage.TotalTokens)
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
			"type":            "response.reasoning_summary_text.delta",
			"output_index":    1,
			"summary_index":   0,
			"sequence_number": 4,
			"item_id":         "rs_1",
			"delta":           "first summary",
		}),
		mustDecodeStreamEvent(t, map[string]any{
			"type":            "response.reasoning_summary_text.delta",
			"output_index":    2,
			"summary_index":   0,
			"sequence_number": 5,
			"item_id":         "rs_2",
			"delta":           "second summary",
		}),
		mustDecodeStreamEvent(t, map[string]any{
			"type":            "response.reasoning_text.delta",
			"output_index":    1,
			"content_index":   0,
			"sequence_number": 6,
			"item_id":         "rs_1",
			"delta":           "private thought",
		}),
		mustDecodeStreamEvent(t, map[string]any{
			"type":            "response.output_text.delta",
			"output_index":    3,
			"content_index":   0,
			"sequence_number": 7,
			"item_id":         "msg_1",
			"delta":           "Hello",
			"logprobs":        []any{},
		}),
		mustDecodeStreamEvent(t, map[string]any{
			"type":            "response.completed",
			"sequence_number": 8,
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
	var reasoningDeltas []chat.ReasoningDelta
	for _, ev := range events {
		err := processStreamEvent(ev, state, true, func(event chat.StreamEvent) error {
			if event.Delta != "" {
				textDeltas = append(textDeltas, event.Delta)
			}
			if event.ReasoningDelta != nil {
				reasoningDeltas = append(reasoningDeltas, *event.ReasoningDelta)
				if event.Raw == nil {
					t.Fatalf("reasoning event must preserve its raw event")
				}
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
	if state.text.String() != "Hello" {
		t.Fatalf("unexpected accumulated stream text: %q", state.text.String())
	}
	if len(reasoningDeltas) != 3 {
		t.Fatalf("unexpected reasoning deltas: %#v", reasoningDeltas)
	}
	if reasoningDeltas[0].Type != chat.ReasoningDeltaSummary || reasoningDeltas[0].Index != 0 || reasoningDeltas[0].Delta != "first summary" {
		t.Fatalf("unexpected first summary delta: %#v", reasoningDeltas[0])
	}
	if reasoningDeltas[1].Type != chat.ReasoningDeltaSummary || reasoningDeltas[1].Index != 1 || reasoningDeltas[1].Delta != "second summary" {
		t.Fatalf("unexpected second summary delta: %#v", reasoningDeltas[1])
	}
	if reasoningDeltas[2].Type != chat.ReasoningDeltaThinking || reasoningDeltas[2].Index != 0 || reasoningDeltas[2].Delta != "private thought" {
		t.Fatalf("unexpected thinking delta: %#v", reasoningDeltas[2])
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
	if got := state.toolCalls[0].Arguments; got != `{"city":"Tokyo"}` {
		t.Fatalf("unexpected accumulated tool call arguments: %q", got)
	}
	if state.completed == nil || state.completed.ID != "resp_456" {
		t.Fatalf("expected completed response, got %#v", state.completed)
	}
	result, err := finalizeStreamResult(state)
	if err != nil {
		t.Fatalf("finalize stream result: %v", err)
	}
	if result.Reasoning == nil || len(result.Reasoning.Summary) != 2 || result.Reasoning.Summary[0] != "first summary" || result.Reasoning.Summary[1] != "second summary" {
		t.Fatalf("unexpected fallback summaries: %#v", result.Reasoning)
	}
	if len(result.Reasoning.Blocks) != 1 || result.Reasoning.Blocks[0].Text != "private thought" {
		t.Fatalf("unexpected fallback thinking: %#v", result.Reasoning)
	}
}

func TestFinalizeStreamResultKeepsCompletedReasoningAuthoritative(t *testing.T) {
	state := &responseStreamState{
		toolCalls: map[int]streamToolCallState{},
		completed: mustDecodeResponse(t, map[string]any{
			"id":                  "resp_reasoning",
			"model":               "gpt-5.4",
			"object":              "response",
			"parallel_tool_calls": true,
			"status":              "completed",
			"output": []any{
				map[string]any{
					"id":      "rs_1",
					"type":    "reasoning",
					"status":  "completed",
					"summary": []any{map[string]any{"type": "summary_text", "text": "complete summary"}},
					"content": []any{map[string]any{"type": "reasoning_text", "text": "complete thought"}},
				},
			},
			"usage": map[string]any{
				"input_tokens":          1,
				"input_tokens_details":  map[string]any{},
				"output_tokens":         1,
				"output_tokens_details": map[string]any{},
				"total_tokens":          2,
			},
			"text": map[string]any{"format": map[string]any{"type": "text"}},
		}),
	}
	state.summaries.append(responseReasoningKey{itemID: "rs_1", outputIndex: 0, partIndex: 0}, "stream summary")
	state.thinking.append(responseReasoningKey{itemID: "rs_1", outputIndex: 0, partIndex: 0}, "stream thought")

	result, err := finalizeStreamResult(state)
	if err != nil {
		t.Fatalf("finalize stream result: %v", err)
	}
	if result.Reasoning == nil || len(result.Reasoning.Summary) != 1 || result.Reasoning.Summary[0] != "complete summary" {
		t.Fatalf("completed summary was not authoritative: %#v", result.Reasoning)
	}
	if len(result.Reasoning.Blocks) != 1 || result.Reasoning.Blocks[0].Text != "complete thought" {
		t.Fatalf("completed thinking was not authoritative: %#v", result.Reasoning)
	}
}

func TestFinalizeStreamResultFallsBackToAccumulatedTextDelta(t *testing.T) {
	state := &responseStreamState{
		toolCalls: map[int]streamToolCallState{},
		completed: mustDecodeResponse(t, map[string]any{
			"id":                  "resp_789",
			"model":               "gpt-5.4",
			"object":              "response",
			"parallel_tool_calls": true,
			"temperature":         1,
			"tool_choice":         "auto",
			"tools":               []any{},
			"top_p":               1,
			"status":              "completed",
			"output":              []any{},
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
		}),
	}
	state.text.WriteString(`{"ok":true}`)

	result, err := finalizeStreamResult(state)
	if err != nil {
		t.Fatalf("finalizeStreamResult: %v", err)
	}
	if result.Text != `{"ok":true}` {
		t.Fatalf("unexpected fallback text: %q", result.Text)
	}
	if len(result.Parts) != 1 || result.Parts[0].Text != `{"ok":true}` {
		t.Fatalf("unexpected fallback parts: %#v", result.Parts)
	}
	if result.Usage.TotalTokens != 11 {
		t.Fatalf("unexpected usage: %#v", result.Usage)
	}
}

func TestFinalizeStreamResultFallsBackToAccumulatedToolCalls(t *testing.T) {
	state := &responseStreamState{
		toolCalls: map[int]streamToolCallState{
			0: {
				CallID:    "call_1",
				ItemID:    "fc_1",
				Name:      "get_weather",
				Arguments: `{"city":"Tokyo"}`,
			},
		},
		completed: mustDecodeResponse(t, map[string]any{
			"id":                  "resp_790",
			"model":               "gpt-5.4",
			"object":              "response",
			"parallel_tool_calls": true,
			"temperature":         1,
			"tool_choice":         "auto",
			"tools":               []any{},
			"top_p":               1,
			"status":              "completed",
			"output":              []any{},
			"usage": map[string]any{
				"input_tokens":         8,
				"input_tokens_details": map[string]any{},
				"output_tokens":        0,
				"output_tokens_details": map[string]any{
					"reasoning_tokens": 0,
				},
				"total_tokens": 8,
			},
			"text": map[string]any{
				"format": map[string]any{"type": "text"},
			},
		}),
	}

	result, err := finalizeStreamResult(state)
	if err != nil {
		t.Fatalf("finalizeStreamResult: %v", err)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("unexpected fallback tool calls: %#v", result.ToolCalls)
	}
	if result.ToolCalls[0].ID != "call_1" || result.ToolCalls[0].Function.Name != "get_weather" || result.ToolCalls[0].Function.Arguments != `{"city":"Tokyo"}` {
		t.Fatalf("unexpected fallback tool call: %#v", result.ToolCalls[0])
	}
	if len(result.Messages) != 1 || len(result.Messages[0].ToolCalls) != 1 {
		t.Fatalf("unexpected fallback messages: %#v", result.Messages)
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

func writeOpenAIResponseSSE(t *testing.T, w http.ResponseWriter, payload map[string]any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal sse payload: %v", err)
	}
	if _, err := w.Write(append(append([]byte("data: "), data...), []byte("\n\n")...)); err != nil {
		t.Fatalf("write sse: %v", err)
	}
}
