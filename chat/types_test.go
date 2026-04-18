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
		WithInferenceProvider("openai"),
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
	if req.InferenceProvider != "openai" {
		t.Fatalf("inference provider not set")
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

func TestAssistantToolCallsClonesInput(t *testing.T) {
	toolCalls := []ToolCall{
		{
			ID: "call_1",
			Function: ToolCallFunction{
				Name:      "lookup",
				Arguments: `{"q":"hello"}`,
			},
			ThoughtSignature: "sig_1",
		},
	}

	msg := AssistantToolCalls(toolCalls...)
	toolCalls[0].Function.Name = "mutated"
	toolCalls[0].ThoughtSignature = "mutated"

	if msg.Role != RoleAssistant {
		t.Fatalf("unexpected role: %q", msg.Role)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Function.Name != "lookup" {
		t.Fatalf("assistant tool calls should clone input, got %#v", msg.ToolCalls[0])
	}
	if msg.ToolCalls[0].ThoughtSignature != "sig_1" {
		t.Fatalf("unexpected thought signature: %#v", msg.ToolCalls[0])
	}
}

func TestToolResultValuePreservesJSONObject(t *testing.T) {
	msg, err := ToolResultValue("call_1", map[string]any{
		"content": "hello",
		"lines":   2,
	})
	if err != nil {
		t.Fatalf("tool result value: %v", err)
	}
	if msg.Role != RoleTool || msg.ToolCallID != "call_1" {
		t.Fatalf("unexpected tool result message: %#v", msg)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(msg.Content), &out); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	if out["content"] != "hello" || out["lines"] != float64(2) {
		t.Fatalf("unexpected content payload: %#v", out)
	}
	if _, exists := out["result"]; exists {
		t.Fatalf("object payload should not be wrapped: %#v", out)
	}
}

func TestToolResultValueWrapsScalarAsResultObject(t *testing.T) {
	msg, err := ToolResultValue("call_1", "hello")
	if err != nil {
		t.Fatalf("tool result value: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(msg.Content), &out); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	if out["result"] != "hello" {
		t.Fatalf("expected scalar payload to be wrapped, got %#v", out)
	}
}

func TestToolResultValueWrapsJSONArrayAsResultObject(t *testing.T) {
	msg, err := ToolResultValue("call_1", json.RawMessage(`[1,2,3]`))
	if err != nil {
		t.Fatalf("tool result value: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(msg.Content), &out); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	result, ok := out["result"].([]any)
	if !ok || len(result) != 3 {
		t.Fatalf("expected array payload to be wrapped, got %#v", out)
	}
}

func TestToolResultValueRejectsInvalidRawJSON(t *testing.T) {
	_, err := ToolResultValue("call_1", json.RawMessage(`{`))
	if err == nil {
		t.Fatalf("expected invalid raw JSON error")
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
