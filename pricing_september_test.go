package uniai

import (
	"testing"

	imagepkg "github.com/quailyquaily/uniai/image"
)

func TestClaudeOpus55Pricing(t *testing.T) {
	catalog := DefaultPricingCatalog()
	for _, model := range []string{"claude-opus-5-5", "claude-opus-5.5"} {
		for _, input := range []int{1000, 900000} {
			usage := Usage{InputTokens: input, OutputTokens: 300, Cache: UsageCache{
				CachedInputTokens: 200, CacheCreationInputTokens: 100,
				Details: map[string]int{"ephemeral_1h_input_tokens": 40},
			}}
			cost, ok := catalog.EstimateChatCost(model, usage)
			if !ok {
				t.Fatalf("%s: missing price", model)
			}
			assertNearlyEqual(t, cost.Input, float64(input-300)*4/1e6)
			assertNearlyEqual(t, cost.CachedInput, 200*0.20/1e6)
			assertNearlyEqual(t, cost.CacheCreationInput, (60*5.0+40*8.0)/1e6)
			assertNearlyEqual(t, cost.Output, 300*20.0/1e6)
			assertNearlyEqual(t, cost.Total, (float64(input-300)*4+40+620+6000)/1e6)
		}
	}
}

func TestSeptemberChatPricing(t *testing.T) {
	catalog := DefaultPricingCatalog()
	usage := Usage{InputTokens: 1000, OutputTokens: 300, Cache: UsageCache{CachedInputTokens: 200}}
	for _, tt := range []struct {
		model                 string
		input, cached, output float64
	}{
		{"deepseek-flash", 0.15, 0.003, 0.60},
		{"deepseek-v4-flash", 0.15, 0.003, 0.60},
		{"deepseek-v4-flash-vision-exp", 0.15, 0.003, 0.60},
		{"@cf/deepseek-ai/deepseek-v4-flash-0731", 0.44, 0.014, 1.32},
		{"glm-5.3-flash", 0.15, 0.03, 0.50},
		{"glm-5.3-flashx", 0.37, 0.075, 1.25},
	} {
		t.Run(tt.model, func(t *testing.T) {
			cost, ok := catalog.EstimateChatCost(tt.model, usage)
			if !ok {
				t.Fatal("missing price")
			}
			assertNearlyEqual(t, cost.Input, 800*tt.input/1e6)
			assertNearlyEqual(t, cost.CachedInput, 200*tt.cached/1e6)
			assertNearlyEqual(t, cost.Output, 300*tt.output/1e6)
		})
	}
	for _, model := range []string{"deepseek-flash", "deepseek-v4-flash", "deepseek-v4-flash-vision-exp"} {
		rule := catalog.findChatPricingRule(model)
		if rule == nil || rule.PeakRates == nil || rule.PeakRates.CachedInputUSDPerMillion == nil {
			t.Errorf("%s: missing peak prices", model)
			continue
		}
		assertNearlyEqual(t, rule.PeakRates.InputUSDPerMillion, 0.30)
		assertNearlyEqual(t, *rule.PeakRates.CachedInputUSDPerMillion, 0.006)
		assertNearlyEqual(t, rule.PeakRates.OutputUSDPerMillion, 1.20)
	}
}

func TestSeptemberImagePricing(t *testing.T) {
	catalog := DefaultPricingCatalog()
	usage := imagepkg.CreateImageUsage{InputTokens: 15, InputTextTokens: 10, InputImageTokens: 5, CachedTextTokens: 2, CachedImageTokens: 1, OutputTokens: 100}
	for _, model := range []string{"gpt-image-2.5-sunburst", "gpt-image-2.5-flare", "gpt-image-2.5-sunburst-2026-09-08", "gpt-image-2.5-flare-2026-09-08"} {
		cost, ok := catalog.EstimateImageCost(model, usage)
		if !ok {
			t.Errorf("%s: missing image price", model)
			continue
		}
		assertNearlyEqual(t, cost.Input, (8*5.0+4*8.0)/1e6)
		assertNearlyEqual(t, cost.CachedInput, (2*1.25+1*2.0)/1e6)
		assertNearlyEqual(t, cost.Output, 100*30.0/1e6)
	}
}
