package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
)

func TestGPT6ChatParameters(t *testing.T) {
	temperature, topP, maxTokens := 0.7, 0.9, 1024
	for _, explicit := range []bool{false, true} {
		opts := structs.JSONMap{"prompt_cache_retention": "24h", "logprobs": true, "top_logprobs": 3}
		if explicit {
			opts["prompt_cache_options"] = map[string]any{"mode": "explicit", "ttl": "30m"}
		}
		req := &chat.Request{
			Messages: []chat.Message{chat.SystemParts(chat.WithPartCacheControl(chat.TextPart("stable"), chat.CacheControl{})), chat.User("hello")},
			Options:  chat.Options{Temperature: &temperature, TopP: &topP, MaxTokens: &maxTokens, OpenAI: opts},
		}
		params, err := buildParams(req, "gpt-6-astra")
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
		for _, key := range []string{"temperature", "top_p", "logprobs", "top_logprobs", "prompt_cache_retention", "max_tokens"} {
			if _, ok := payload[key]; ok {
				t.Errorf("unexpected %s in %s", key, data)
			}
		}
		if payload["max_completion_tokens"] != float64(maxTokens) {
			t.Errorf("max_completion_tokens missing: %s", data)
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

func TestGPT6ChatReasoningEffort(t *testing.T) {
	for _, effort := range []string{"", "low", "medium", "high", "xhigh", "max", "none", "minimal", "invalid"} {
		for _, raw := range []bool{false, true} {
			req := &chat.Request{Model: "openai/gpt-6-astra", Messages: []chat.Message{chat.User("hello")}}
			value := chat.ReasoningEffort(effort)
			if raw {
				req.Options.OpenAI = structs.JSONMap{"reasoning_effort": effort}
			} else {
				req.Options.ReasoningEffort = &value
			}
			_, err := buildParams(req, "")
			wantError := effort == "none" || effort == "minimal" || effort == "invalid"
			if (err != nil) != wantError {
				t.Errorf("effort=%q raw=%v: %v", effort, raw, err)
			}
		}
	}
}

func TestGPT6ChatRejectsTools(t *testing.T) {
	req := &chat.Request{Messages: []chat.Message{chat.User("hello")}, Tools: []chat.Tool{chat.FunctionTool("lookup", "", nil)}}
	_, err := buildParams(req, "gpt-6-astra")
	if err == nil || !strings.Contains(err.Error(), "openai_resp") {
		t.Fatalf("expected Responses routing error, got %v", err)
	}
}
