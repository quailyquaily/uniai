package uniai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/chat"
)

func TestParseToolDecisionSingle(t *testing.T) {
	calls, err := parseToolDecision(`{"tool":"get_weather","arguments":{"city":"Tokyo"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "get_weather" {
		t.Fatalf("unexpected tool name: %s", calls[0].Name)
	}
	assertArgs(t, calls[0].Arguments, map[string]any{"city": "Tokyo"})
}

func TestParseToolDecisionMultiple(t *testing.T) {
	input := `{"tools":[{"tool":"a","arguments":{"x":1}},{"tool":"b","arguments":{}}]}`
	calls, err := parseToolDecision(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Name != "a" || calls[1].Name != "b" {
		t.Fatalf("unexpected tool order: %s, %s", calls[0].Name, calls[1].Name)
	}
	assertArgs(t, calls[0].Arguments, map[string]any{"x": float64(1)})
	assertArgs(t, calls[1].Arguments, map[string]any{})
}

func TestParseToolDecisionInvalidTool(t *testing.T) {
	_, err := parseToolDecision(`{"tool":123,"arguments":{}}`)
	if err == nil {
		t.Fatalf("expected error for invalid tool type")
	}
}

func TestParseToolDecisionEmpty(t *testing.T) {
	cases := []string{"", "   ", "no tool needed"}
	for _, input := range cases {
		calls, err := parseToolDecision(input)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", input, err)
		}
		if len(calls) != 0 {
			t.Fatalf("expected no calls for %q, got %d", input, len(calls))
		}
	}
}

func TestBuildToolDecisionPrompt(t *testing.T) {
	req := &chat.Request{
		Tools: []chat.Tool{
			FunctionTool("get_weather", "Get weather", []byte(`{
				"type":"object",
				"properties":{"city":{"type":"string"}},
				"required":["city"]
			}`)),
		},
		ToolChoice: &chat.ToolChoice{Mode: "required"},
	}
	prompt, err := buildToolDecisionPrompt(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "get_weather") {
		t.Fatalf("prompt missing tool name")
	}
	if !strings.Contains(prompt, "MUST call at least one tool") {
		t.Fatalf("prompt missing required tool instruction")
	}
}

func TestEnforceToolChoice(t *testing.T) {
	err := enforceToolChoice(&chat.ToolChoice{Mode: "none"}, []emulatedToolCall{{Name: "a"}})
	if err == nil {
		t.Fatalf("expected error for tool_choice none")
	}
	err = enforceToolChoice(&chat.ToolChoice{Mode: "required"}, nil)
	if err == nil {
		t.Fatalf("expected error for tool_choice required with no calls")
	}
	err = enforceToolChoice(&chat.ToolChoice{Mode: "function", FunctionName: "a"}, []emulatedToolCall{{Name: "b"}})
	if err == nil {
		t.Fatalf("expected error for tool_choice function mismatch")
	}
}

func TestMergeChatUsageAggregatesFields(t *testing.T) {
	usage := mergeChatUsage(
		chat.Usage{
			InputTokens:  10,
			OutputTokens: 2,
			TotalTokens:  12,
			Cache: chat.UsageCache{
				CachedInputTokens:        3,
				CacheCreationInputTokens: 1,
				Details: map[string]int{
					"ephemeral_5m_input_tokens": 1,
				},
			},
			Cost: &chat.UsageCost{Currency: "USD", Total: 1},
		},
		chat.Usage{
			InputTokens:  7,
			OutputTokens: 4,
			TotalTokens:  11,
			Cache: chat.UsageCache{
				CachedInputTokens:        2,
				CacheCreationInputTokens: 5,
				Details: map[string]int{
					"ephemeral_5m_input_tokens": 2,
					"ephemeral_1h_input_tokens": 3,
				},
			},
		},
	)

	if usage.InputTokens != 17 || usage.OutputTokens != 6 || usage.TotalTokens != 23 {
		t.Fatalf("unexpected aggregate usage: %#v", usage)
	}
	if usage.Cache.CachedInputTokens != 5 || usage.Cache.CacheCreationInputTokens != 6 {
		t.Fatalf("unexpected aggregate cache usage: %#v", usage.Cache)
	}
	if usage.Cache.Details["ephemeral_5m_input_tokens"] != 3 || usage.Cache.Details["ephemeral_1h_input_tokens"] != 3 {
		t.Fatalf("unexpected aggregate cache details: %#v", usage.Cache.Details)
	}
	if usage.Cost != nil {
		t.Fatalf("expected aggregate usage to clear cost, got %#v", usage.Cost)
	}
}

func TestWrapPrefixedChatStreamUsageMergesPrefixCostAfterFinalRequestPricing(t *testing.T) {
	client := New(Config{
		Provider:    "openai",
		OpenAIModel: "gpt-5.4",
		Pricing: &PricingCatalog{
			Chat: []ChatPricingRule{
				{
					InferenceProvider: "openai",
					Model:             "gpt-5.4",
					Tiers: []ChatPricingTier{
						{
							MaxInputTokens:      intPtr(270000),
							InputUSDPerMillion:  2.50,
							OutputUSDPerMillion: 15.00,
						},
						{
							InputUSDPerMillion:  5.00,
							OutputUSDPerMillion: 22.50,
						},
					},
				},
			},
		},
	})
	req := &chat.Request{Model: "gpt-5.4"}

	var got *chat.Usage
	onStream := wrapPrefixedChatStreamUsage(chat.Usage{
		InputTokens: 200000,
		TotalTokens: 200000,
		Cost: &chat.UsageCost{
			Currency:  "USD",
			Estimated: true,
			Input:     0.5,
			Total:     0.5,
		},
	}, true, func(ev chat.StreamEvent) error {
		got = ev.Usage
		return nil
	})
	wrapped := client.wrapChatStreamCost("openai", req, onStream)

	if err := wrapped(chat.StreamEvent{
		Done: true,
		Usage: &chat.Usage{
			InputTokens:  100000,
			OutputTokens: 0,
			TotalTokens:  100000,
		},
	}); err != nil {
		t.Fatalf("wrapped stream: %v", err)
	}
	if got == nil {
		t.Fatal("expected usage on final event")
	}
	if got.InputTokens != 300000 || got.OutputTokens != 0 || got.TotalTokens != 300000 {
		t.Fatalf("unexpected streamed aggregate usage: %#v", got)
	}
	if got.Cost == nil {
		t.Fatal("expected aggregate streamed cost")
	}
	if got.Cost.Total != 0.75 {
		t.Fatalf("unexpected streamed aggregate cost: %#v", got.Cost)
	}
}

func TestChatToolEmulationFallbackAggregatesUsageAcrossInternalCalls(t *testing.T) {
	var callCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		switch callCount {
		case 1:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "chatcmpl_native",
				"object":  "chat.completion",
				"created": 0,
				"model":   "gpt-4.1-mini",
				"choices": []map[string]any{
					{
						"index": 0,
						"message": map[string]any{
							"role":    "assistant",
							"content": "native path without tool call",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]any{
					"prompt_tokens":     10,
					"completion_tokens": 2,
					"total_tokens":      12,
				},
			})
		case 2:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "chatcmpl_decision",
				"object":  "chat.completion",
				"created": 0,
				"model":   "gpt-4.1-mini",
				"choices": []map[string]any{
					{
						"index": 0,
						"message": map[string]any{
							"role":    "assistant",
							"content": "JSON_BEGIN\n{\"tools\":[]}\nJSON_END",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]any{
					"prompt_tokens":     5,
					"completion_tokens": 1,
					"total_tokens":      6,
				},
			})
		case 3:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "chatcmpl_final",
				"object":  "chat.completion",
				"created": 0,
				"model":   "gpt-4.1-mini",
				"choices": []map[string]any{
					{
						"index": 0,
						"message": map[string]any{
							"role":    "assistant",
							"content": "final answer",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]any{
					"prompt_tokens":     7,
					"completion_tokens": 3,
					"total_tokens":      10,
				},
			})
		default:
			t.Fatalf("unexpected request count: %d", callCount)
		}
	}))
	defer server.Close()

	client := New(Config{
		Provider:      "openai",
		OpenAIAPIKey:  "test-key",
		OpenAIAPIBase: server.URL + "/v1",
		OpenAIModel:   "gpt-4.1-mini",
		Pricing: &PricingCatalog{
			Chat: []ChatPricingRule{
				{
					InferenceProvider:   "openai",
					Model:               "gpt-4.1-mini",
					InputUSDPerMillion:  1,
					OutputUSDPerMillion: 2,
				},
			},
		},
	})

	resp, err := client.Chat(context.Background(),
		WithMessages(chat.User("hello")),
		WithTools([]Tool{
			FunctionTool("get_weather", "Get weather", []byte(`{"type":"object"}`)),
		}),
		WithToolsEmulationMode(ToolsEmulationFallback),
	)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if callCount != 3 {
		t.Fatalf("expected 3 internal calls, got %d", callCount)
	}
	if resp.Text != "final answer" {
		t.Fatalf("unexpected text: %q", resp.Text)
	}
	if resp.Usage.InputTokens != 22 || resp.Usage.OutputTokens != 6 || resp.Usage.TotalTokens != 28 {
		t.Fatalf("unexpected aggregate usage: %#v", resp.Usage)
	}
	if resp.Usage.Cost == nil || resp.Usage.Cost.Total != 0.000034 {
		t.Fatalf("unexpected aggregate cost: %#v", resp.Usage.Cost)
	}
	if len(resp.Warnings) == 0 || resp.Warnings[0] != "tool calls emulated" {
		t.Fatalf("unexpected warnings: %#v", resp.Warnings)
	}
}

func TestChatToolEmulationFallbackKeepsTierSelectionPerInternalRequest(t *testing.T) {
	var callCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		switch callCount {
		case 1:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "chatcmpl_native",
				"object":  "chat.completion",
				"created": 0,
				"model":   "gpt-5.4",
				"choices": []map[string]any{
					{
						"index": 0,
						"message": map[string]any{
							"role":    "assistant",
							"content": "native path without tool call",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]any{
					"prompt_tokens":     200000,
					"completion_tokens": 0,
					"total_tokens":      200000,
				},
			})
		case 2:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "chatcmpl_decision",
				"object":  "chat.completion",
				"created": 0,
				"model":   "gpt-5.4",
				"choices": []map[string]any{
					{
						"index": 0,
						"message": map[string]any{
							"role":    "assistant",
							"content": "JSON_BEGIN\n{\"tools\":[]}\nJSON_END",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]any{
					"prompt_tokens":     100000,
					"completion_tokens": 0,
					"total_tokens":      100000,
				},
			})
		case 3:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "chatcmpl_final",
				"object":  "chat.completion",
				"created": 0,
				"model":   "gpt-5.4",
				"choices": []map[string]any{
					{
						"index": 0,
						"message": map[string]any{
							"role":    "assistant",
							"content": "final answer",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]any{
					"prompt_tokens":     1,
					"completion_tokens": 1,
					"total_tokens":      2,
				},
			})
		default:
			t.Fatalf("unexpected request count: %d", callCount)
		}
	}))
	defer server.Close()

	client := New(Config{
		Provider:      "openai",
		OpenAIAPIKey:  "test-key",
		OpenAIAPIBase: server.URL + "/v1",
		OpenAIModel:   "gpt-5.4",
		Pricing: &PricingCatalog{
			Chat: []ChatPricingRule{
				{
					InferenceProvider: "openai",
					Model:             "gpt-5.4",
					Tiers: []ChatPricingTier{
						{
							MaxInputTokens:      intPtr(270000),
							InputUSDPerMillion:  2.50,
							OutputUSDPerMillion: 15.00,
						},
						{
							InputUSDPerMillion:  5.00,
							OutputUSDPerMillion: 22.50,
						},
					},
				},
			},
		},
	})

	resp, err := client.Chat(context.Background(),
		WithMessages(chat.User("hello")),
		WithTools([]Tool{
			FunctionTool("get_weather", "Get weather", []byte(`{"type":"object"}`)),
		}),
		WithToolsEmulationMode(ToolsEmulationFallback),
	)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if callCount != 3 {
		t.Fatalf("expected 3 internal calls, got %d", callCount)
	}
	if resp.Usage.Cost == nil {
		t.Fatal("expected aggregate cost")
	}
	if resp.Usage.InputTokens != 300001 || resp.Usage.OutputTokens != 1 {
		t.Fatalf("unexpected aggregate usage: %#v", resp.Usage)
	}
	assertNearlyEqual(t, resp.Usage.Cost.Input, 0.7500025)
	assertNearlyEqual(t, resp.Usage.Cost.Output, 0.000015)
	assertNearlyEqual(t, resp.Usage.Cost.Total, 0.7500175)
}

func TestChatToolEmulationFallbackLeavesAggregateCostNilWhenOneInternalRequestCannotBePriced(t *testing.T) {
	var callCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		switch callCount {
		case 1:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "chatcmpl_native",
				"object":  "chat.completion",
				"created": 0,
				"model":   "gpt-5.2",
				"choices": []map[string]any{
					{
						"index": 0,
						"message": map[string]any{
							"role":    "assistant",
							"content": "native path without tool call",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]any{
					"prompt_tokens":     10,
					"completion_tokens": 1,
					"total_tokens":      11,
				},
			})
		case 2:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "chatcmpl_decision",
				"object":  "chat.completion",
				"created": 0,
				"model":   "gpt-5.2",
				"choices": []map[string]any{
					{
						"index": 0,
						"message": map[string]any{
							"role":    "assistant",
							"content": "JSON_BEGIN\n{\"tools\":[]}\nJSON_END",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]any{
					"prompt_tokens":     5,
					"completion_tokens": 1,
					"total_tokens":      6,
					"prompt_tokens_details": map[string]any{
						"cached_tokens": 2,
					},
				},
			})
		case 3:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "chatcmpl_final",
				"object":  "chat.completion",
				"created": 0,
				"model":   "gpt-5.2",
				"choices": []map[string]any{
					{
						"index": 0,
						"message": map[string]any{
							"role":    "assistant",
							"content": "final answer",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]any{
					"prompt_tokens":     7,
					"completion_tokens": 1,
					"total_tokens":      8,
				},
			})
		default:
			t.Fatalf("unexpected request count: %d", callCount)
		}
	}))
	defer server.Close()

	client := New(Config{
		Provider:      "openai",
		OpenAIAPIKey:  "test-key",
		OpenAIAPIBase: server.URL + "/v1",
		OpenAIModel:   "gpt-5.2",
		Pricing: &PricingCatalog{
			Chat: []ChatPricingRule{
				{
					InferenceProvider:   "openai",
					Model:               "gpt-5.2",
					InputUSDPerMillion:  1.25,
					OutputUSDPerMillion: 10.00,
				},
			},
		},
	})

	resp, err := client.Chat(context.Background(),
		WithMessages(chat.User("hello")),
		WithTools([]Tool{
			FunctionTool("get_weather", "Get weather", []byte(`{"type":"object"}`)),
		}),
		WithToolsEmulationMode(ToolsEmulationFallback),
	)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if callCount != 3 {
		t.Fatalf("expected 3 internal calls, got %d", callCount)
	}
	if resp.Usage.Cost != nil {
		t.Fatalf("expected aggregate cost to stay nil, got %#v", resp.Usage.Cost)
	}
}

func assertArgs(t *testing.T, raw json.RawMessage, expected map[string]any) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("failed to unmarshal args: %v", err)
	}
	if len(got) != len(expected) {
		t.Fatalf("args size mismatch: got=%v expected=%v", got, expected)
	}
	for k, v := range expected {
		if got[k] != v {
			t.Fatalf("args mismatch for %s: got=%v expected=%v", k, got[k], v)
		}
	}
}
