package azure

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
	openai "github.com/openai/openai-go/v3"
)

func TestApplyAzureOptionsMapsPromptCacheRetention(t *testing.T) {
	params := openai.ChatCompletionNewParams{}
	applyAzureOptions(&params, structs.JSONMap{
		"prompt_cache_retention": "24h",
	}, nil)

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if !strings.Contains(string(data), `"prompt_cache_retention":"24h"`) {
		t.Fatalf("expected prompt_cache_retention in payload, got %s", string(data))
	}
}

func TestToResultReadsCachedInputTokens(t *testing.T) {
	resp := &openai.ChatCompletion{
		Model: "deployment",
		Usage: openai.CompletionUsage{
			PromptTokens:     10,
			CompletionTokens: 3,
			TotalTokens:      13,
			PromptTokensDetails: openai.CompletionUsagePromptTokensDetails{
				CachedTokens: 4,
			},
		},
		Choices: []openai.ChatCompletionChoice{
			{
				Message: openai.ChatCompletionMessage{
					Content: "hello",
				},
			},
		},
	}

	result := toResult(resp)
	if result.Usage.Cache.CachedInputTokens != 4 {
		t.Fatalf("unexpected cache usage: %#v", result.Usage.Cache)
	}
}

func TestToResultReadsTopLevelCachedTokensFallback(t *testing.T) {
	var resp openai.ChatCompletion
	if err := json.Unmarshal([]byte(`{
		"model": "deployment",
		"choices": [
			{
				"message": {
					"role": "assistant",
					"content": "hello"
				}
			}
		],
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 3,
			"total_tokens": 13,
			"cached_tokens": 5
		}
	}`), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	result := toResult(&resp)
	if result.Usage.Cache.CachedInputTokens != 5 {
		t.Fatalf("unexpected cache usage: %#v", result.Usage.Cache)
	}
}
