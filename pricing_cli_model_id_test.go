package uniai

import (
	"fmt"
	"testing"
)

// Issue #24: model IDs reported by coding CLIs must resolve to catalog rules.

func TestDefaultPricingCatalogFable52Rates(t *testing.T) {
	catalog := DefaultPricingCatalog()
	usage := Usage{InputTokens: 1000, OutputTokens: 300, TotalTokens: 1300, Cache: UsageCache{
		CachedInputTokens: 200, CacheCreationInputTokens: 100,
		Details: map[string]int{"ephemeral_1h_input_tokens": 40},
	}}
	for _, model := range []string{"claude-fable-5-2", "claude-mythos-5-2", "claude-fable-5-2[1m]", "anthropic/claude-fable-5-2"} {
		t.Run(model, func(t *testing.T) {
			cost, ok := catalog.EstimateChatCostWithInferenceProvider("anthropic", model, usage)
			if !ok {
				t.Fatal("expected cost estimate")
			}
			assertNearlyEqual(t, cost.Input, 700*8.00/1e6)
			assertNearlyEqual(t, cost.CachedInput, 200*0.20/1e6)
			assertNearlyEqual(t, cost.CacheCreationInput, (60*10.00+40*16.00)/1e6)
			assertNearlyEqual(t, cost.Output, 300*40.00/1e6)
		})
	}
}

func TestDefaultPricingCatalogCodingCLIModelIDs(t *testing.T) {
	catalog := DefaultPricingCatalog()
	usage := Usage{InputTokens: 100000, OutputTokens: 1000, TotalTokens: 101000}
	for _, tt := range []struct{ provider, model, base string }{
		{"", "claude-sonnet-5-5[1m]", "claude-sonnet-5-5"},
		{"anthropic", "claude-sonnet-5-5[1m]", "claude-sonnet-5-5"},
		{"", "Claude-Opus-5-5[1M]", "claude-opus-5-5"},
		{"", "anthropic/claude-sonnet-5-5[1m]", "claude-sonnet-5-5"},
		{"", "claude-sonnet-4-6[1m]", "claude-sonnet-4-6"},
		{"", "gemini-3.8-flash-high", "gemini-3.8-flash"},
		{"gemini", "gemini-3.8-flash-low", "gemini-3.8-flash"},
		{"", "models/gemini-3.8-flash-medium", "gemini-3.8-flash"},
		{"", "google/gemini-3.8-flash-high", "gemini-3.8-flash"},
	} {
		t.Run(tt.model, func(t *testing.T) {
			rule := catalog.findChatPricingRuleWithInferenceProvider(tt.provider, tt.model)
			if rule == nil || rule.Model != tt.base {
				t.Fatalf("rule = %+v, want %s", rule, tt.base)
			}
			got, ok := catalog.EstimateChatCostWithInferenceProvider(tt.provider, tt.model, usage)
			want, wantOK := catalog.EstimateChatCostWithInferenceProvider(tt.provider, tt.base, usage)
			if !ok || !wantOK {
				t.Fatalf("ok = %v, base ok = %v", ok, wantOK)
			}
			assertNearlyEqual(t, got.Total, want.Total)
		})
	}
}

func TestCodingCLIModelIDKeepsContextTiers(t *testing.T) {
	catalog := DefaultPricingCatalog()
	for _, tt := range []struct {
		input         int
		inRate, outRt float64
	}{
		{99999, 0.10, 0.50},
		{100000, 0.10, 0.50},
		{100001, 0.50, 2.50},
	} {
		t.Run(fmt.Sprint(tt.input), func(t *testing.T) {
			cost, ok := catalog.EstimateChatCost("claude-haiku-5-5[1m]", Usage{InputTokens: tt.input, OutputTokens: 100, TotalTokens: tt.input + 100})
			if !ok {
				t.Fatal("expected tiered cost estimate")
			}
			assertNearlyEqual(t, cost.Input, float64(tt.input)*tt.inRate/1e6)
			assertNearlyEqual(t, cost.Output, 100*tt.outRt/1e6)
		})
	}
}

func TestCodingCLIModelIDDoesNotGuess(t *testing.T) {
	catalog := DefaultPricingCatalog()
	for _, model := range []string{
		"claude-haiku-5-5-20261001", // 5.x IDs are dateless; no dated ID is documented
		"gemini-3.8-pro-high",       // gemini-3.8-pro has no published price
		"gemini-3.8-pro",
		"gemini-3.8-flash-minimal",
		"gemini-3.8-flash-high-high",
		"gemini-3.8-flash[2m]",
		"gpt-6.1-sol-high", // effort suffix is only stripped for gemini- models
		"claude-opus-5-5-high",
		"claude-unknown[1m]",
		"gemini-high",
		"[1m]",
	} {
		if rule := catalog.findChatPricingRule(model); rule != nil {
			t.Errorf("%s: unexpectedly priced by %s", model, rule.Model)
		}
	}
	// The suffix fallback is chat-only.
	if rule := catalog.findImagePricingRule("gemini-3.1-flash-image-high"); rule != nil {
		t.Errorf("image lookup stripped an effort suffix: %s", rule.Model)
	}
}

func TestCodingCLIModelIDPrefersExplicitRules(t *testing.T) {
	catalog := &PricingCatalog{Chat: []ChatPricingRule{
		{InferenceProvider: "gemini", Model: "gemini-x-flash", InputUSDPerMillion: 1, OutputUSDPerMillion: 2},
		{InferenceProvider: "gemini", Model: "gemini-x-flash-high", InputUSDPerMillion: 3, OutputUSDPerMillion: 4},
		{InferenceProvider: "anthropic", Model: "claude-x", InputUSDPerMillion: 5, OutputUSDPerMillion: 6},
		{InferenceProvider: "anthropic", Model: "claude-x-long", Aliases: []string{"claude-x[1m]"}, InputUSDPerMillion: 7, OutputUSDPerMillion: 8},
		{InferenceProvider: "other", Model: "claude-y", InputUSDPerMillion: 9, OutputUSDPerMillion: 10},
	}}
	if err := catalog.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ provider, model, want string }{
		{"", "gemini-x-flash-high", "gemini-x-flash-high"},
		{"", "gemini-x-flash-low", "gemini-x-flash"},
		{"", "claude-x[1m]", "claude-x-long"},
		{"anthropic", "claude-x[1m]", "claude-x-long"},
		{"other", "claude-y[1m]", "claude-y"},
		{"anthropic", "claude-y[1m]", ""}, // provider hint still bounds the lookup
		{"anthropic", "gemini-x-flash-high", ""},
	} {
		rule := catalog.findChatPricingRuleWithInferenceProvider(tt.provider, tt.model)
		got := ""
		if rule != nil {
			got = rule.Model
		}
		if got != tt.want {
			t.Errorf("%s/%s: matched %q, want %q", tt.provider, tt.model, got, tt.want)
		}
	}
}

// Dated Claude IDs resolve only through documented snapshot aliases.
func TestDefaultPricingCatalogDatedClaudeIDs(t *testing.T) {
	catalog := DefaultPricingCatalog()
	for model, want := range map[string]string{
		"claude-opus-4-5-20251101":   "claude-opus-4-5",
		"claude-sonnet-4-5-20250929": "claude-sonnet-4-5",
		"claude-haiku-4-5-20251001":  "claude-haiku-4-5",
		"claude-haiku-5-5-20261001":  "", // 4.6 and later IDs are dateless
		"claude-sonnet-5-5-20260928": "",
		"claude-fable-5-2-20261109":  "",
	} {
		got := ""
		if rule := catalog.findChatPricingRuleWithInferenceProvider("anthropic", model); rule != nil {
			got = rule.Model
		}
		if got != want {
			t.Errorf("%s: matched %q, want %q", model, got, want)
		}
	}
}
