package oaicompat

import (
	"encoding/json"
	"testing"

	openai "github.com/openai/openai-go/v3"
)

func TestEnsureChatCompletionStreamIncludesUsage(t *testing.T) {
	params := openai.ChatCompletionNewParams{
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeObfuscation: openai.Bool(false),
			IncludeUsage:       openai.Bool(false),
		},
	}

	ensureChatCompletionStreamIncludesUsage(&params)

	if !params.StreamOptions.IncludeUsage.Valid() || !params.StreamOptions.IncludeUsage.Value {
		t.Fatalf("expected include_usage=true, got %#v", params.StreamOptions.IncludeUsage)
	}
	if !params.StreamOptions.IncludeObfuscation.Valid() || params.StreamOptions.IncludeObfuscation.Value {
		t.Fatalf("expected include_obfuscation to stay false, got %#v", params.StreamOptions.IncludeObfuscation)
	}
}

func TestAccumulatedToResultReadsTopLevelCachedTokensFallback(t *testing.T) {
	var resp openai.ChatCompletion
	if err := json.Unmarshal([]byte(`{
		"model": "kimi-k2.6",
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
			"cached_tokens": 7
		}
	}`), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	result := accumulatedToResult(&resp)
	if result.Usage.Cache.CachedInputTokens != 7 {
		t.Fatalf("unexpected cache usage: %#v", result.Usage.Cache)
	}
}
