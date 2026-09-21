package openairesp

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/openai/openai-go/v3/responses"
	"github.com/quailyquaily/uniai/chat"
)

func TestGPT6ResponsesParameters(t *testing.T) {
	temperature, topP := 0.7, 0.9
	for _, explicit := range []bool{false, true} {
		opts := structs.JSONMap{
			"prompt_cache_retention": "24h",
			"top_logprobs":           3,
			"include":                []string{"message.output_text.logprobs", "reasoning.encrypted_content"},
			"reasoning":              map[string]any{"effort": "high", "mode": "pro", "context": "all_turns"},
		}
		if explicit {
			opts["prompt_cache_options"] = map[string]any{"mode": "explicit", "ttl": "30m"}
		}
		req := &chat.Request{
			Messages: []chat.Message{chat.SystemParts(chat.WithPartCacheControl(chat.TextPart("stable"), chat.CacheControl{})), chat.User("hello")},
			Tools:    []chat.Tool{chat.FunctionTool("lookup", "", nil)},
			Options:  chat.Options{Temperature: &temperature, TopP: &topP, OpenAI: opts},
		}
		params, err := buildParams(req, "gpt-6-astra", false)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"temperature", "top_p", "top_logprobs", "prompt_cache_retention"} {
			if _, ok := payload[key]; ok {
				t.Errorf("unexpected %s in %s", key, data)
			}
		}
		if strings.Contains(string(data), "message.output_text.logprobs") {
			t.Errorf("unsupported logprobs include: %s", data)
		}
		if !strings.Contains(string(data), "reasoning.encrypted_content") || len(params.Tools) != 1 {
			t.Errorf("lost reasoning or tools: %s", data)
		}
		cache, ok := payload["prompt_cache_options"].(map[string]any)
		if !ok || cache["ttl"] != "30m" || (explicit && cache["mode"] != "explicit") {
			t.Errorf("wrong cache options: %s", data)
		}
		if !strings.Contains(string(data), `"prompt_cache_breakpoint":{"mode":"explicit"}`) {
			t.Errorf("missing cache breakpoint: %s", data)
		}
	}
}

func TestGPT6ResponsesReasoningValidation(t *testing.T) {
	for _, effort := range []string{"", "low", "medium", "high", "xhigh", "max", "none", "minimal", "invalid"} {
		for _, raw := range []bool{false, true} {
			req := &chat.Request{Model: "gpt-6-astra", Messages: []chat.Message{chat.User("hello")}}
			value := chat.ReasoningEffort(effort)
			if raw {
				req.Options.OpenAI = structs.JSONMap{"reasoning": map[string]any{"effort": effort}}
			} else {
				req.Options.ReasoningEffort = &value
			}
			_, err := buildParams(req, "", false)
			wantError := effort == "none" || effort == "minimal" || effort == "invalid"
			if (err != nil) != wantError {
				t.Errorf("effort=%q raw=%v: %v", effort, raw, err)
			}
		}
	}
	for _, field := range []string{"mode", "context"} {
		req := &chat.Request{Model: "gpt-6-astra", Messages: []chat.Message{chat.User("hello")}, Options: chat.Options{OpenAI: structs.JSONMap{"reasoning": map[string]any{field: "invalid"}}}}
		if _, err := buildParams(req, "", false); err == nil || !strings.Contains(err.Error(), "reasoning "+field) {
			t.Errorf("expected invalid reasoning %s error, got %v", field, err)
		}
	}
}

func TestGPT6ResponsesRawCacheBreakpoint(t *testing.T) {
	req := &chat.Request{Model: "gpt-6-astra", Options: chat.Options{OpenAI: structs.JSONMap{
		"input": []any{map[string]any{"role": "system", "content": []any{map[string]any{"type": "input_text", "text": "stable", "prompt_cache_breakpoint": map[string]any{"mode": "explicit"}}}}},
	}}}
	if _, err := buildParams(req, "", false); err != nil {
		t.Fatal(err)
	}
}

func TestGPT6ResponsesIncludePreservation(t *testing.T) {
	for _, tt := range []struct {
		name    string
		include []responses.ResponseIncludable
		want    []responses.ResponseIncludable
	}{
		{name: "nil"},
		{name: "empty", include: []responses.ResponseIncludable{}},
		{name: "logprobs_only", include: []responses.ResponseIncludable{"message.output_text.logprobs", "message.output_text.logprobs"}},
		{
			name:    "order_and_duplicates",
			include: []responses.ResponseIncludable{"message.output_text.logprobs", "reasoning.encrypted_content", "web_search_call.action.sources", "message.output_text.logprobs", "reasoning.encrypted_content"},
			want:    []responses.ResponseIncludable{"reasoning.encrypted_content", "web_search_call.action.sources", "reasoning.encrypted_content"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			original := slices.Clone(tt.include)
			req := &chat.Request{
				Messages: []chat.Message{chat.User("hello")},
				Options:  chat.Options{OpenAI: structs.JSONMap{"include": tt.include}},
			}
			params, err := buildParams(req, "gpt-6-astra", false)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(params.Include, tt.want) {
				t.Errorf("include = %v, want %v", params.Include, tt.want)
			}
			if !slices.Equal(tt.include, original) {
				t.Errorf("caller include changed: %v, want %v", tt.include, original)
			}
			legacy, err := buildParams(req, "gpt-5.6-sol", false)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(legacy.Include, original) {
				t.Errorf("legacy include = %v, want %v", legacy.Include, original)
			}
		})
	}
}
