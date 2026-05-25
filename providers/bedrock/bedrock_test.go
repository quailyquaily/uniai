package bedrock

import (
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
)

func TestToBedrockContentMapsCacheControl(t *testing.T) {
	content, err := toBedrockContent(chat.UserParts(
		chat.WithPartCacheControl(chat.TextPart("prefix"), chat.CacheTTL5m()),
		chat.TextPart("suffix"),
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(content))
	}
	if content[0].CacheControl == nil || content[0].CacheControl.TTL != "5m" {
		t.Fatalf("unexpected cache control: %#v", content[0].CacheControl)
	}
	if content[1].CacheControl != nil {
		t.Fatalf("expected second content block to have no cache control")
	}
}

func TestValidateBedrockCacheControl(t *testing.T) {
	req := &chat.Request{
		Messages: []chat.Message{
			chat.UserParts(chat.WithPartCacheControl(chat.TextPart("prefix"), chat.CacheTTL5m())),
		},
	}
	if err := validateBedrockCacheControl(req, "anthropic.claude-sonnet-4-20250514-v1:0"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := validateBedrockCacheControl(req, "amazon.nova-pro-v1:0"); err == nil {
		t.Fatalf("expected error for non-anthropic model")
	}

	systemReq := &chat.Request{
		Messages: []chat.Message{
			chat.SystemParts(chat.WithPartCacheControl(chat.TextPart("system"), chat.CacheTTL5m())),
			chat.User("hello"),
		},
	}
	if err := validateBedrockCacheControl(systemReq, "anthropic.claude-sonnet-4-20250514-v1:0"); err == nil {
		t.Fatalf("expected error for system cache control")
	}

	toolReq := &chat.Request{
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Tools: []chat.Tool{
			chat.WithToolCacheControl(chat.FunctionTool("lookup", "desc", []byte(`{"type":"object"}`)), chat.CacheTTL5m()),
		},
	}
	if err := validateBedrockCacheControl(toolReq, "anthropic.claude-sonnet-4-20250514-v1:0"); err == nil {
		t.Fatalf("expected error for tool cache control")
	}
}

func TestToBedrockContentRejectsEmptyCachedTextPart(t *testing.T) {
	_, err := toBedrockContent(chat.UserParts(
		chat.WithPartCacheControl(chat.TextPart(" "), chat.CacheTTL5m()),
	))
	if err == nil {
		t.Fatalf("expected error")
	}
	if got := err.Error(); !strings.Contains(got, "non-empty text part") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBedrockOpus47ModelOverlayDropsTopK(t *testing.T) {
	payload := map[string]any{}
	applyBedrockOptions(payload, structs.JSONMap{"top_k": 5})
	applyBedrockModelOverlay(payload, "anthropic.claude-opus-4.7-v1:0")
	if _, ok := payload["top_k"]; ok {
		t.Fatalf("expected Opus 4.7 top_k to be omitted, got %#v", payload)
	}
}

func TestBedrockModelOverlayKeepsTopKForOpus46(t *testing.T) {
	payload := map[string]any{}
	applyBedrockOptions(payload, structs.JSONMap{"top_k": 5})
	applyBedrockModelOverlay(payload, "anthropic.claude-opus-4-6-v1:0")
	if payload["top_k"] != 5 {
		t.Fatalf("expected Opus 4.6 top_k to be preserved, got %#v", payload)
	}
}

func TestParseBedrockUsageReadsCacheMetrics(t *testing.T) {
	usage := parseBedrockUsage(bedrockUsage{
		InputTokens:              100,
		OutputTokens:             12,
		CacheReadInputTokens:     80,
		CacheCreationInputTokens: 40,
		CacheCreation: map[string]int{
			"ephemeral_5m_input_tokens": 40,
		},
	})
	if usage.InputTokens != 100 || usage.OutputTokens != 12 || usage.TotalTokens != 112 {
		t.Fatalf("unexpected usage: %#v", usage)
	}
	if usage.Cache.CachedInputTokens != 80 || usage.Cache.CacheCreationInputTokens != 40 {
		t.Fatalf("unexpected cache usage: %#v", usage.Cache)
	}
	if usage.Cache.Details["ephemeral_5m_input_tokens"] != 40 {
		t.Fatalf("unexpected cache details: %#v", usage.Cache.Details)
	}
}
