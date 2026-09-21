package uniai

import (
	"fmt"
	"strings"
	"testing"

	imagepkg "github.com/quailyquaily/uniai/image"
)

func TestDefaultPricingCatalogCurrentFlatRates(t *testing.T) {
	catalog := DefaultPricingCatalog()
	usage := Usage{
		InputTokens: 1000, OutputTokens: 300, TotalTokens: 1300,
		Cache: UsageCache{CachedInputTokens: 200},
	}
	for _, tt := range []struct {
		provider, model       string
		input, cached, output float64
	}{
		{"zai", "glm-5.3-flash", 0.15, 0.03, 0.50},
		{"moonshot", "kimi-k2.7-code", 0.95, 0.19, 4.00},
		{"moonshot", "kimi-k2.7-code-highspeed", 1.90, 0.38, 8.00},
		{"sakana", "sakana-namazu-v1.0", 0.95, 0.15, 4.00},
		{"mistral", "mistral-large-2512", 0.50, 0.05, 1.50},
		{"mistral", "mistral-large-latest", 0.50, 0.05, 1.50},
		{"mistral", "mistral-small-2603", 0.15, 0.015, 0.60},
		{"mistral", "mistral-medium-3-5", 1.50, 0.15, 7.50},
		{"mistral", "mistral-medium-3", 1.50, 0.15, 7.50},
		{"mistral", "mistral-medium-latest", 1.50, 0.15, 7.50},
		{"deepseek", "deepseek-v4-flash-vision-exp", 0.15, 0.003, 0.60},
		{"deepseek", "@cf/deepseek-ai/deepseek-v4-flash-0731", 0.44, 0.014, 1.32},
		{"deepseek", "@cf/deepseek-ai/deepseek-v4-pro-0813", 1.32, 0.044, 3.96},
		{"zai", "@cf/zai-org/glm-5.2", 1.40, 0.26, 4.40},
		{"zai", "@cf/zai-org/glm-5.3", 1.40, 0.26, 4.40},
		{"zai", "@cf/zai-org/glm-5.3-flash", 0.15, 0.03, 0.50},
		{"moonshot", "@cf/moonshotai/kimi-k2.6", 0.95, 0.16, 4.00},
		{"moonshot", "@cf/moonshotai/kimi-k2.7-code", 0.95, 0.19, 4.00},
	} {
		t.Run(tt.model, func(t *testing.T) {
			if strings.HasPrefix(tt.model, "@cf/") {
				rule := catalog.findChatPricingRule(tt.model)
				if rule == nil || rule.Model != tt.model {
					t.Fatal("expected explicit Cloudflare pricing, not a vendor suffix fallback")
				}
			}
			cost, ok := catalog.EstimateChatCostWithInferenceProvider(tt.provider, tt.model, usage)
			if !ok {
				t.Fatal("expected cost estimate including cached input")
			}
			assertNearlyEqual(t, cost.Input, 800*tt.input/1_000_000)
			assertNearlyEqual(t, cost.CachedInput, 200*tt.cached/1_000_000)
			assertNearlyEqual(t, cost.Output, 300*tt.output/1_000_000)
			assertNearlyEqual(t, cost.Total, (800*tt.input+200*tt.cached+300*tt.output)/1_000_000)
		})
	}
}

func TestDefaultPricingCatalogCurrentTierBoundaries(t *testing.T) {
	catalog := DefaultPricingCatalog()
	for _, tt := range []struct {
		provider, model                   string
		maxInput                          int
		input, cached, output             float64
		longInput, longCached, longOutput float64
	}{
		{"openai", "gpt-5.4", 272000, 2.50, 0.25, 15.00, 5.00, 0.50, 22.50},
		{"openai", "gpt-5.4-pro", 272000, 30.00, 0, 180.00, 60.00, 0, 270.00},
		{"openai", "gpt-5.5", 272000, 5.00, 0.50, 30.00, 10.00, 1.00, 45.00},
		{"openai", "gpt-5.5-pro", 272000, 30.00, 0, 180.00, 60.00, 0, 270.00},
		{"minimax", "MiniMax-M3", 512000, 0.30, 0.06, 1.20, 0.60, 0.12, 2.40},
		{"sakana", "fugu-ultra-v1.0", 272000, 5.00, 0.50, 30.00, 10.00, 1.00, 45.00},
		{"sakana", "fugu-ultra-v1.1", 272000, 5.00, 0.50, 30.00, 10.00, 1.00, 45.00},
	} {
		for _, inputTokens := range []int{tt.maxInput - 1, tt.maxInput, tt.maxInput + 1} {
			t.Run(fmt.Sprintf("%s/%d", tt.model, inputTokens), func(t *testing.T) {
				inputRate, cachedRate, outputRate := tt.input, tt.cached, tt.output
				if inputTokens > tt.maxInput {
					inputRate, cachedRate, outputRate = tt.longInput, tt.longCached, tt.longOutput
				}
				cachedTokens := 1000
				if cachedRate == 0 {
					cachedTokens = 0 // Pro models do not publish cached-input rates.
				}
				cost, ok := catalog.EstimateChatCostWithInferenceProvider(tt.provider, tt.model, Usage{
					InputTokens: inputTokens, OutputTokens: 100, TotalTokens: inputTokens + 100,
					Cache: UsageCache{CachedInputTokens: cachedTokens},
				})
				if !ok {
					t.Fatal("expected tiered cost estimate")
				}
				wantInput := float64(inputTokens-cachedTokens) * inputRate / 1_000_000
				wantCached := float64(cachedTokens) * cachedRate / 1_000_000
				wantOutput := 100 * outputRate / 1_000_000
				assertNearlyEqual(t, cost.Input, wantInput)
				assertNearlyEqual(t, cost.CachedInput, wantCached)
				assertNearlyEqual(t, cost.Output, wantOutput)
				assertNearlyEqual(t, cost.Total, wantInput+wantCached+wantOutput)
			})
		}
	}
}

func TestDefaultPricingCatalogDeepSeekVisionPeakRates(t *testing.T) {
	catalog := DefaultPricingCatalog()
	rule := catalog.findChatPricingRule("deepseek-v4-flash-vision-exp")
	if rule == nil || rule.PeakRates == nil || rule.PeakRates.CachedInputUSDPerMillion == nil {
		t.Fatal("expected vision model peak rates")
	}
	assertNearlyEqual(t, rule.PeakRates.InputUSDPerMillion, 0.30)
	assertNearlyEqual(t, *rule.PeakRates.CachedInputUSDPerMillion, 0.006)
	assertNearlyEqual(t, rule.PeakRates.OutputUSDPerMillion, 1.20)
}

func TestDefaultPricingCatalogGPTImage15TextOutput(t *testing.T) {
	catalog := DefaultPricingCatalog()
	cost, ok := catalog.EstimateImageCostWithInferenceProvider("openai", "gpt-image-1.5", imagepkg.CreateImageUsage{
		InputTokens: 10, InputTextTokens: 10,
		OutputTokens: 120, OutputTextTokens: 20, OutputImageTokens: 100, TotalTokens: 130,
	})
	if !ok {
		t.Fatal("expected image cost estimate with separate text output pricing")
	}
	assertNearlyEqual(t, cost.Input, 10*5.00/1_000_000)
	assertNearlyEqual(t, cost.Output, (20*10.00+100*32.00)/1_000_000)
	assertNearlyEqual(t, cost.Total, 0.00345)
}
