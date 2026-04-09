package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
)

func TestBuildRequestRequiresMessages(t *testing.T) {
	_, err := BuildRequest(WithModel("gpt-4.1-mini"))
	if err == nil {
		t.Fatalf("expected error when messages are missing")
	}
}

func TestBuildRequestAllowsOpenAIInputWithoutMessages(t *testing.T) {
	_, err := BuildRequest(
		WithProvider("openai_resp"),
		WithOpenAIOptions(structs.JSONMap{
			"input": []map[string]any{
				{
					"role":    "user",
					"content": "hello",
				},
			},
		}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWithMessagesAppend(t *testing.T) {
	req, err := BuildRequest(
		WithMessages(User("first")),
		WithMessages(User("second")),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Content != "first" || req.Messages[1].Content != "second" {
		t.Fatalf("unexpected order: %+v", req.Messages)
	}
}

func TestWithReplaceMessages(t *testing.T) {
	req, err := BuildRequest(
		WithMessages(User("first")),
		WithReplaceMessages(User("only")),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.Messages) != 1 || req.Messages[0].Content != "only" {
		t.Fatalf("unexpected messages: %+v", req.Messages)
	}
}

func TestOptions(t *testing.T) {
	req, err := BuildRequest(
		WithMessages(User("hi")),
		WithTemperature(0.7),
		WithTopP(0.9),
		WithMaxTokens(123),
		WithStop("END"),
		WithPresencePenalty(0.1),
		WithFrequencyPenalty(0.2),
		WithUser("u1"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Options.Temperature == nil || *req.Options.Temperature != 0.7 {
		t.Fatalf("temperature not set")
	}
	if req.Options.TopP == nil || *req.Options.TopP != 0.9 {
		t.Fatalf("top_p not set")
	}
	if req.Options.MaxTokens == nil || *req.Options.MaxTokens != 123 {
		t.Fatalf("max_tokens not set")
	}
	if len(req.Options.Stop) != 1 || req.Options.Stop[0] != "END" {
		t.Fatalf("stop not set")
	}
	if req.Options.PresencePenalty == nil || *req.Options.PresencePenalty != 0.1 {
		t.Fatalf("presence penalty not set")
	}
	if req.Options.FrequencyPenalty == nil || *req.Options.FrequencyPenalty != 0.2 {
		t.Fatalf("frequency penalty not set")
	}
	if req.Options.User == nil || *req.Options.User != "u1" {
		t.Fatalf("user not set")
	}
}

func TestReasoningOptions(t *testing.T) {
	req, err := BuildRequest(
		WithMessages(User("hi")),
		WithReasoningEffort(ReasoningEffortHigh),
		WithReasoningBudgetTokens(4096),
		WithReasoningDetails(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Options.ReasoningEffort == nil || *req.Options.ReasoningEffort != ReasoningEffortHigh {
		t.Fatalf("reasoning effort not set")
	}
	if req.Options.ReasoningBudget == nil || *req.Options.ReasoningBudget != 4096 {
		t.Fatalf("reasoning budget not set")
	}
	if !req.Options.ReasoningDetails {
		t.Fatalf("reasoning details not enabled")
	}
}

func TestBuildRequestRejectsInvalidCacheControlTTL(t *testing.T) {
	_, err := BuildRequest(
		WithMessages(UserParts(WithPartCacheControl(TextPart("prefix"), CacheControl{TTL: "30m"}))),
	)
	if err == nil {
		t.Fatalf("expected error")
	}
	if err.Error() == "" || !strings.Contains(err.Error(), "unsupported cache ttl") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCacheControlHelpers(t *testing.T) {
	part := WithPartCacheControl(TextPart("prefix"), CacheTTL5m())
	if part.CacheControl == nil || part.CacheControl.TTL != "5m" {
		t.Fatalf("unexpected part cache control: %#v", part.CacheControl)
	}

	tool := WithToolCacheControl(FunctionTool("lookup", "desc", []byte(`{"type":"object"}`)), CacheTTL1h())
	if tool.CacheControl == nil || tool.CacheControl.TTL != "1h" {
		t.Fatalf("unexpected tool cache control: %#v", tool.CacheControl)
	}
}

func TestUsageMarshalJSONOmitsEmptyCache(t *testing.T) {
	data, err := json.Marshal(Usage{
		InputTokens:  1,
		OutputTokens: 2,
		TotalTokens:  3,
	})
	if err != nil {
		t.Fatalf("marshal usage: %v", err)
	}
	if strings.Contains(string(data), `"cache"`) {
		t.Fatalf("expected empty cache to be omitted, got %s", string(data))
	}
}

func TestUsageMarshalJSONIncludesCacheWhenPresent(t *testing.T) {
	data, err := json.Marshal(Usage{
		InputTokens:  1,
		OutputTokens: 2,
		TotalTokens:  3,
		Cache: UsageCache{
			CachedInputTokens: 5,
		},
	})
	if err != nil {
		t.Fatalf("marshal usage: %v", err)
	}
	if !strings.Contains(string(data), `"cache":{"cached_input_tokens":5}`) {
		t.Fatalf("expected cache details in payload, got %s", string(data))
	}
}

func TestUsageMarshalJSONIncludesCostWhenPresent(t *testing.T) {
	data, err := json.Marshal(Usage{
		InputTokens:  1,
		OutputTokens: 2,
		TotalTokens:  3,
		Cost: &UsageCost{
			Currency:  "USD",
			Estimated: true,
			Total:     0.000123,
		},
	})
	if err != nil {
		t.Fatalf("marshal usage: %v", err)
	}
	if !strings.Contains(string(data), `"cost":{"currency":"USD","estimated":true,"total":0.000123}`) {
		t.Fatalf("expected cost details in payload, got %s", string(data))
	}
}
