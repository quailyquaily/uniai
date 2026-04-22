package oaicompat

import (
	"encoding/json"
	"strings"

	openai "github.com/openai/openai-go/v3"
	"github.com/quailyquaily/uniai/chat"
)

// ChatCompletionCachedInputTokens extracts cache-hit prompt tokens from an
// OpenAI-compatible Chat Completions usage payload.
//
// Standard OpenAI responses use prompt_tokens_details.cached_tokens. Some
// compatible backends instead return a top-level cached_tokens field under
// usage, so this falls back to that shape only when the standard field is not
// present.
func ChatCompletionCachedInputTokens(usage openai.CompletionUsage) int {
	if usage.PromptTokensDetails.JSON.CachedTokens.Valid() {
		return int(usage.PromptTokensDetails.CachedTokens)
	}

	raw := strings.TrimSpace(usage.RawJSON())
	if raw == "" {
		if usage.PromptTokensDetails.CachedTokens > 0 {
			return int(usage.PromptTokensDetails.CachedTokens)
		}
		return 0
	}

	var fallback struct {
		CachedTokens *int64 `json:"cached_tokens"`
	}
	if err := json.Unmarshal([]byte(raw), &fallback); err != nil || fallback.CachedTokens == nil {
		return 0
	}
	return int(*fallback.CachedTokens)
}

func ChatCompletionUsageToChatUsage(usage openai.CompletionUsage) chat.Usage {
	return chat.Usage{
		InputTokens:  int(usage.PromptTokens),
		OutputTokens: int(usage.CompletionTokens),
		TotalTokens:  int(usage.TotalTokens),
		Cache: chat.UsageCache{
			CachedInputTokens: ChatCompletionCachedInputTokens(usage),
		},
	}
}
