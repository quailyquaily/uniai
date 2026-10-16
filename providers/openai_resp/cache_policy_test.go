package openairesp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
)

// These cases mirror the OpenAI cache compatibility table in docs/cache.md.
func TestPromptCachePolicyByModel(t *testing.T) {
	cacheOptionModels := []string{"gpt-5.6", "gpt-6-sol", "gpt-6.1-sol", "openai/gpt-6.1-sol", "GPT-6.1-Sol-2026-09-30"}
	for _, model := range cacheOptionModels {
		for _, tc := range []struct {
			name          string
			opts          structs.JSONMap
			wantRetention any
			wantOptions   any
		}{
			{"legacy_in_memory", structs.JSONMap{"prompt_cache_retention": "in_memory"}, nil, map[string]any{"ttl": "30m"}},
			{"legacy_24h", structs.JSONMap{"prompt_cache_retention": "24h"}, nil, map[string]any{"ttl": "30m"}},
			{"both_new_wins", structs.JSONMap{"prompt_cache_retention": "24h", "prompt_cache_options": map[string]any{"mode": "explicit"}}, nil, map[string]any{"mode": "explicit"}},
			{"new_only", structs.JSONMap{"prompt_cache_options": map[string]any{"ttl": "30m"}}, nil, map[string]any{"ttl": "30m"}},
			{"omitted", nil, nil, nil},
		} {
			t.Run(model+"/"+tc.name, func(t *testing.T) {
				payload := buildCachePayload(t, model, tc.opts)
				assertPayloadField(t, payload, "prompt_cache_retention", tc.wantRetention)
				assertPayloadField(t, payload, "prompt_cache_options", tc.wantOptions)
			})
		}
		t.Run(model+"/breakpoint", func(t *testing.T) {
			req := &chat.Request{Model: model, Messages: []chat.Message{chat.UserParts(chat.WithPartCacheControl(chat.TextPart("stable"), chat.CacheControl{}))}}
			params, err := buildParams(req, "", false)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(params)
			if !strings.Contains(string(data), `"prompt_cache_breakpoint":{"mode":"explicit"}`) {
				t.Errorf("missing breakpoint: %s", data)
			}
		})
		t.Run(model+"/unsupported_ttl", func(t *testing.T) {
			req := &chat.Request{Model: model, Messages: []chat.Message{chat.User("hi")}, Options: chat.Options{OpenAI: structs.JSONMap{"prompt_cache_options": map[string]any{"ttl": "24h"}}}}
			if _, err := buildParams(req, "", false); err == nil || !strings.Contains(err.Error(), "ttl must be 30m") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestPromptCacheOptionsRejectedForLegacyModels(t *testing.T) {
	for _, model := range []string{"gpt-5.5", "gpt-5.5-pro", "openai/gpt-5.4", "gpt-5.4-mini"} {
		req := &chat.Request{Model: model, Messages: []chat.Message{chat.User("hi")}, Options: chat.Options{OpenAI: structs.JSONMap{"prompt_cache_options": map[string]any{"ttl": "30m"}}}}
		if _, err := buildParams(req, "", false); err == nil || !strings.Contains(err.Error(), "does not support prompt_cache_options") {
			t.Errorf("%s: err = %v", model, err)
		}
		req.Messages = []chat.Message{chat.UserParts(chat.WithPartCacheControl(chat.TextPart("stable"), chat.CacheControl{}))}
		req.Options.OpenAI = nil
		if _, err := buildParams(req, "", false); err == nil {
			t.Errorf("%s: expected breakpoint rejection", model)
		}
	}
}

func TestPromptCachePolicyUnknownModelPassesThrough(t *testing.T) {
	payload := buildCachePayload(t, "my-gateway-alias", structs.JSONMap{
		"prompt_cache_retention": "24h",
		"prompt_cache_options":   map[string]any{"ttl": "30m"},
	})
	assertPayloadField(t, payload, "prompt_cache_retention", "24h")
	assertPayloadField(t, payload, "prompt_cache_options", map[string]any{"ttl": "30m"})
}

func TestPromptCachePolicyUnknownModelKeepsGenericValidation(t *testing.T) {
	req := &chat.Request{Model: "my-gateway-alias", Messages: []chat.Message{chat.User("hi")}, Options: chat.Options{OpenAI: structs.JSONMap{"prompt_cache_options": map[string]any{"ttl": "24h"}}}}
	if _, err := buildParams(req, "", false); err == nil || !strings.Contains(err.Error(), `prompt_cache_options.ttl must be 30m, got "24h"`) {
		t.Fatalf("err = %v", err)
	}
}

func buildCachePayload(t *testing.T, model string, opts structs.JSONMap) map[string]any {
	t.Helper()
	req := &chat.Request{Model: model, Messages: []chat.Message{chat.User("hi")}, Options: chat.Options{OpenAI: opts}}
	params, err := buildParams(req, "", false)
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
	return payload
}

func assertPayloadField(t *testing.T, payload map[string]any, key string, want any) {
	t.Helper()
	got, ok := payload[key]
	if want == nil {
		if ok {
			t.Errorf("unexpected %s = %#v", key, got)
		}
		return
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("%s = %s, want %s", key, gotJSON, wantJSON)
	}
}

func TestPromptCachePolicyCodexDropsCacheFields(t *testing.T) {
	for _, model := range []string{"gpt-5.5", "gpt-6.1-sol"} {
		req := &chat.Request{Model: model, Messages: []chat.Message{chat.User("hi")}, Options: chat.Options{OpenAI: structs.JSONMap{
			"prompt_cache_retention": "24h",
			"prompt_cache_options":   map[string]any{"ttl": "30m"},
		}}}
		params, err := buildParams(req, "", true)
		if err != nil {
			t.Fatalf("%s: %v", model, err)
		}
		data, _ := json.Marshal(params)
		if strings.Contains(string(data), "prompt_cache_retention") || strings.Contains(string(data), "prompt_cache_options") {
			t.Errorf("%s: codex payload kept cache fields: %s", model, data)
		}
	}
}
