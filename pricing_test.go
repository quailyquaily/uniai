package uniai

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/chat"
)

func TestParsePricingYAML(t *testing.T) {
	catalog, err := ParsePricingYAML([]byte(`
pricing_references:
  inference_providers:
    openai: https://platform.openai.com/docs/pricing/
chat:
  - inference_provider: openai
    model: gpt-5.2
    aliases:
      - gpt-5.2-20260401
    input_usd_per_million: 1.25
    output_usd_per_million: 10
    cached_input_usd_per_million: 0.125
`))
	if err != nil {
		t.Fatalf("parse pricing yaml: %v", err)
	}
	if len(catalog.Chat) != 1 {
		t.Fatalf("unexpected chat rule count: %d", len(catalog.Chat))
	}
	rule := catalog.Chat[0]
	if rule.InferenceProvider != "openai" || rule.Model != "gpt-5.2" {
		t.Fatalf("unexpected rule: %#v", rule)
	}
	if len(rule.Aliases) != 1 || rule.Aliases[0] != "gpt-5.2-20260401" {
		t.Fatalf("unexpected aliases: %#v", rule.Aliases)
	}
	if rule.CachedInputUSDPerMillion == nil || *rule.CachedInputUSDPerMillion != 0.125 {
		t.Fatalf("unexpected cached input price: %#v", rule.CachedInputUSDPerMillion)
	}
	if len(rule.Tiers) != 0 {
		t.Fatalf("unexpected tiers: %#v", rule.Tiers)
	}
}

func TestParsePricingYAMLWithTiers(t *testing.T) {
	catalog, err := ParsePricingYAML([]byte(`
chat:
  - inference_provider: openai
    model: gpt-5.4
    tiers:
      - max_input_tokens: 270000
        input_usd_per_million: 2.5
        cached_input_usd_per_million: 0.25
        output_usd_per_million: 15
      - input_usd_per_million: 5
        cached_input_usd_per_million: 0.5
        output_usd_per_million: 22.5
`))
	if err != nil {
		t.Fatalf("parse pricing yaml: %v", err)
	}
	if len(catalog.Chat) != 1 {
		t.Fatalf("unexpected chat rule count: %d", len(catalog.Chat))
	}
	rule := catalog.Chat[0]
	if len(rule.Tiers) != 2 {
		t.Fatalf("unexpected tier count: %#v", rule.Tiers)
	}
	if rule.Tiers[0].MaxInputTokens == nil || *rule.Tiers[0].MaxInputTokens != 270000 {
		t.Fatalf("unexpected first tier max_input_tokens: %#v", rule.Tiers[0].MaxInputTokens)
	}
	if rule.Tiers[1].MaxInputTokens != nil {
		t.Fatalf("unexpected second tier max_input_tokens: %#v", rule.Tiers[1].MaxInputTokens)
	}
}

func TestPricingCatalogEstimateChatCost(t *testing.T) {
	catalog := &PricingCatalog{
		Chat: []ChatPricingRule{
			{
				InferenceProvider:        "openai",
				Model:                    "gpt-5.2",
				InputUSDPerMillion:       1.25,
				OutputUSDPerMillion:      10,
				CachedInputUSDPerMillion: float64Ptr(0.125),
				Aliases:                  []string{"gpt-5.2-20260401"},
			},
		},
	}

	cost, ok := catalog.EstimateChatCost("gpt-5.2-20260401", Usage{
		InputTokens:  100,
		OutputTokens: 25,
		TotalTokens:  125,
		Cache: UsageCache{
			CachedInputTokens: 40,
		},
	})
	if !ok {
		t.Fatal("expected cost estimate")
	}
	assertNearlyEqual(t, cost.Input, 0.000075)
	assertNearlyEqual(t, cost.CachedInput, 0.000005)
	assertNearlyEqual(t, cost.Output, 0.00025)
	assertNearlyEqual(t, cost.Total, 0.00033)
}

func TestPricingCatalogEstimateChatCostUsesShortTierAtBoundary(t *testing.T) {
	catalog := &PricingCatalog{
		Chat: []ChatPricingRule{
			{
				InferenceProvider: "openai",
				Model:             "gpt-5.4",
				Tiers: []ChatPricingTier{
					{
						MaxInputTokens:           intPtr(270000),
						InputUSDPerMillion:       2.50,
						CachedInputUSDPerMillion: float64Ptr(0.25),
						OutputUSDPerMillion:      15.00,
					},
					{
						InputUSDPerMillion:       5.00,
						CachedInputUSDPerMillion: float64Ptr(0.50),
						OutputUSDPerMillion:      22.50,
					},
				},
			},
		},
	}

	cost, ok := catalog.EstimateChatCost("gpt-5.4", Usage{
		InputTokens:  270000,
		OutputTokens: 1000,
		TotalTokens:  271000,
		Cache: UsageCache{
			CachedInputTokens: 1000,
		},
	})
	if !ok {
		t.Fatal("expected short-tier cost estimate")
	}
	assertNearlyEqual(t, cost.Input, 269000*2.50/1_000_000)
	assertNearlyEqual(t, cost.CachedInput, 1000*0.25/1_000_000)
	assertNearlyEqual(t, cost.Output, 1000*15.00/1_000_000)
	assertNearlyEqual(t, cost.Total, 0.68775)
}

func TestPricingCatalogEstimateChatCostUsesLongTierFromRawInputTokens(t *testing.T) {
	catalog := &PricingCatalog{
		Chat: []ChatPricingRule{
			{
				InferenceProvider: "openai",
				Model:             "gpt-5.4",
				Tiers: []ChatPricingTier{
					{
						MaxInputTokens:           intPtr(270000),
						InputUSDPerMillion:       2.50,
						CachedInputUSDPerMillion: float64Ptr(0.25),
						OutputUSDPerMillion:      15.00,
					},
					{
						InputUSDPerMillion:       5.00,
						CachedInputUSDPerMillion: float64Ptr(0.50),
						OutputUSDPerMillion:      22.50,
					},
				},
			},
		},
	}

	cost, ok := catalog.EstimateChatCost("gpt-5.4", Usage{
		InputTokens:  270001,
		OutputTokens: 1000,
		TotalTokens:  271001,
		Cache: UsageCache{
			CachedInputTokens: 1000,
		},
	})
	if !ok {
		t.Fatal("expected long-tier cost estimate")
	}
	assertNearlyEqual(t, cost.Input, 269001*5.00/1_000_000)
	assertNearlyEqual(t, cost.CachedInput, 1000*0.50/1_000_000)
	assertNearlyEqual(t, cost.Output, 1000*22.50/1_000_000)
	assertNearlyEqual(t, cost.Total, 1.368005)
}

func TestPricingCatalogEstimateChatCostWithInferenceProviderPrefersProviderMatch(t *testing.T) {
	catalog := &PricingCatalog{
		Chat: []ChatPricingRule{
			{
				InferenceProvider:   "openai",
				Model:               "gpt-5",
				InputUSDPerMillion:  1.25,
				OutputUSDPerMillion: 10,
			},
			{
				InferenceProvider:   "azure",
				Model:               "gpt-5",
				InputUSDPerMillion:  2.00,
				OutputUSDPerMillion: 12,
			},
		},
	}

	cost, ok := catalog.EstimateChatCostWithInferenceProvider("azure", "gpt-5", Usage{
		InputTokens:  100,
		OutputTokens: 25,
		TotalTokens:  125,
	})
	if !ok {
		t.Fatal("expected cost estimate")
	}
	assertNearlyEqual(t, cost.Input, 0.0002)
	assertNearlyEqual(t, cost.Output, 0.0003)
	assertNearlyEqual(t, cost.Total, 0.0005)
}

func TestPricingCatalogEstimateChatCostWithInferenceProviderFallsBackWhenProviderMissing(t *testing.T) {
	catalog := &PricingCatalog{
		Chat: []ChatPricingRule{
			{
				InferenceProvider:   "openai",
				Model:               "gpt-5",
				InputUSDPerMillion:  1.25,
				OutputUSDPerMillion: 10,
			},
		},
	}

	cost, ok := catalog.EstimateChatCostWithInferenceProvider("anthropic", "gpt-5", Usage{
		InputTokens:  100,
		OutputTokens: 25,
		TotalTokens:  125,
	})
	if !ok {
		t.Fatal("expected fallback cost estimate")
	}
	assertNearlyEqual(t, cost.Input, 0.000125)
	assertNearlyEqual(t, cost.Output, 0.00025)
	assertNearlyEqual(t, cost.Total, 0.000375)
}

func TestPricingCatalogValidateAllowsDuplicateModelNamesAcrossInferenceProviders(t *testing.T) {
	catalog := &PricingCatalog{
		Chat: []ChatPricingRule{
			{
				InferenceProvider:   "openai",
				Model:               "gpt-5",
				InputUSDPerMillion:  1.25,
				OutputUSDPerMillion: 10,
			},
			{
				InferenceProvider:   "azure",
				Model:               "gpt-5",
				InputUSDPerMillion:  2.00,
				OutputUSDPerMillion: 12,
			},
		},
	}

	if err := catalog.Validate(); err != nil {
		t.Fatalf("expected duplicate model names across inference providers to be valid: %v", err)
	}
}

func TestPricingCatalogEstimateChatCostNormalizesVendorPrefixedModelName(t *testing.T) {
	catalog := &PricingCatalog{
		Chat: []ChatPricingRule{
			{
				InferenceProvider:   "openai",
				Model:               "gpt-5",
				InputUSDPerMillion:  1.25,
				OutputUSDPerMillion: 10,
			},
		},
	}

	cost, ok := catalog.EstimateChatCost("ABC/gpt-5", Usage{
		InputTokens:  100,
		OutputTokens: 25,
		TotalTokens:  125,
	})
	if !ok {
		t.Fatal("expected vendor-prefixed model name to match")
	}
	assertNearlyEqual(t, cost.Input, 0.000125)
	assertNearlyEqual(t, cost.Output, 0.00025)
	assertNearlyEqual(t, cost.Total, 0.000375)
}

func TestPricingCatalogEstimateChatCostPrefersExactSlashModelMatch(t *testing.T) {
	catalog := &PricingCatalog{
		Chat: []ChatPricingRule{
			{
				InferenceProvider:   "openai",
				Model:               "gpt-oss-120b",
				InputUSDPerMillion:  9,
				OutputUSDPerMillion: 9,
			},
			{
				InferenceProvider:   "openai",
				Model:               "openai/gpt-oss-120b",
				InputUSDPerMillion:  0.15,
				OutputUSDPerMillion: 0.60,
			},
		},
	}

	cost, ok := catalog.EstimateChatCost("openai/gpt-oss-120b", Usage{
		InputTokens:  100,
		OutputTokens: 25,
		TotalTokens:  125,
	})
	if !ok {
		t.Fatal("expected exact slash model match")
	}
	assertNearlyEqual(t, cost.Input, 0.000015)
	assertNearlyEqual(t, cost.Output, 0.000015)
	assertNearlyEqual(t, cost.Total, 0.00003)
}

func TestPricingCatalogEstimateChatCostAnthropicCacheCreationDetails(t *testing.T) {
	catalog := &PricingCatalog{
		Chat: []ChatPricingRule{
			{
				InferenceProvider:               "anthropic",
				Model:                           "claude-sonnet-4-20250514",
				InputUSDPerMillion:              3,
				OutputUSDPerMillion:             15,
				CachedInputUSDPerMillion:        float64Ptr(0.30),
				CacheCreationInputUSDPerMillion: float64Ptr(3.75),
				CacheCreationInputDetailUSDPerMillion: map[string]float64{
					"ephemeral_5m_input_tokens": 3.75,
				},
			},
		},
	}

	cost, ok := catalog.EstimateChatCost("claude-sonnet-4-20250514", Usage{
		InputTokens:  160,
		OutputTokens: 10,
		TotalTokens:  170,
		Cache: UsageCache{
			CachedInputTokens:        80,
			CacheCreationInputTokens: 40,
			Details: map[string]int{
				"ephemeral_5m_input_tokens": 40,
			},
		},
	})
	if !ok {
		t.Fatal("expected cost estimate")
	}
	assertNearlyEqual(t, cost.Input, 0.00012)
	assertNearlyEqual(t, cost.CachedInput, 0.000024)
	assertNearlyEqual(t, cost.CacheCreationInput, 0.00015)
	assertNearlyEqual(t, cost.Output, 0.00015)
	assertNearlyEqual(t, cost.Total, 0.000444)
}

func TestPricingCatalogEstimateChatCostNoBuiltins(t *testing.T) {
	catalog := &PricingCatalog{}
	if _, ok := catalog.EstimateChatCost("gpt-5.2", Usage{
		InputTokens:  10,
		OutputTokens: 5,
		TotalTokens:  15,
	}); ok {
		t.Fatal("expected empty catalog to produce no cost")
	}
}

func TestDefaultPricingCatalog(t *testing.T) {
	catalog := DefaultPricingCatalog()
	if catalog == nil {
		t.Fatal("expected embedded default pricing catalog")
	}
	if !catalogHasRule(catalog, "gpt-5.4") {
		t.Fatal("expected embedded default pricing catalog to include gpt-5.4")
	}

	catalog.Chat = nil

	again := DefaultPricingCatalog()
	if again == nil {
		t.Fatal("expected cloned embedded default pricing catalog")
	}
	if !catalogHasRule(again, "gpt-5.4") {
		t.Fatal("expected embedded default pricing catalog clone to stay intact")
	}
}

func TestParsePricingYAMLRejectsNonFinitePrices(t *testing.T) {
	_, err := ParsePricingYAML([]byte(`
chat:
  - inference_provider: openai
    model: gpt-5.4
    input_usd_per_million: .nan
    output_usd_per_million: 15
`))
	if err == nil {
		t.Fatal("expected non-finite price to be rejected")
	}
	if !strings.Contains(err.Error(), "finite number") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPricingCatalogValidateRejectsNonFinitePointerAndDetailPrices(t *testing.T) {
	catalog := &PricingCatalog{
		Chat: []ChatPricingRule{
			{
				InferenceProvider:        "anthropic",
				Model:                    "claude-sonnet-4-6",
				InputUSDPerMillion:       3,
				OutputUSDPerMillion:      15,
				CachedInputUSDPerMillion: float64Ptr(math.Inf(1)),
				CacheCreationInputDetailUSDPerMillion: map[string]float64{
					"ephemeral_5m_input_tokens": math.NaN(),
				},
			},
		},
	}

	if err := catalog.Validate(); err == nil {
		t.Fatal("expected validation error for non-finite pricing")
	}
}

func TestPricingCatalogValidateRejectsMixedFlatRatesAndTiers(t *testing.T) {
	catalog := &PricingCatalog{
		Chat: []ChatPricingRule{
			{
				InferenceProvider:   "openai",
				Model:               "gpt-5.4",
				InputUSDPerMillion:  2.50,
				OutputUSDPerMillion: 15.00,
				Tiers: []ChatPricingTier{
					{
						MaxInputTokens:      intPtr(270000),
						InputUSDPerMillion:  2.50,
						OutputUSDPerMillion: 15.00,
					},
				},
			},
		},
	}

	if err := catalog.Validate(); err == nil {
		t.Fatal("expected validation error for mixed flat rates and tiers")
	}
}

func TestPricingCatalogValidateRejectsInvalidTierOrder(t *testing.T) {
	catalog := &PricingCatalog{
		Chat: []ChatPricingRule{
			{
				InferenceProvider: "openai",
				Model:             "gpt-5.4",
				Tiers: []ChatPricingTier{
					{
						MaxInputTokens:      intPtr(300000),
						InputUSDPerMillion:  2.50,
						OutputUSDPerMillion: 15.00,
					},
					{
						MaxInputTokens:      intPtr(270000),
						InputUSDPerMillion:  5.00,
						OutputUSDPerMillion: 22.50,
					},
				},
			},
		},
	}

	if err := catalog.Validate(); err == nil {
		t.Fatal("expected validation error for decreasing tier max_input_tokens")
	}
}

func TestPricingCatalogEstimateChatCostWithoutUsageReturnsNoCost(t *testing.T) {
	catalog := &PricingCatalog{
		Chat: []ChatPricingRule{
			{
				InferenceProvider:   "openai",
				Model:               "gpt-5.2",
				InputUSDPerMillion:  1.25,
				OutputUSDPerMillion: 10,
			},
		},
	}

	if cost, ok := catalog.EstimateChatCost("gpt-5.2", Usage{}); ok || cost != nil {
		t.Fatalf("expected no cost for empty usage, got %#v ok=%v", cost, ok)
	}
}

func TestPricingCatalogEstimateChatCostNormalizesDetailRateKeysAtLookup(t *testing.T) {
	catalog, err := ParsePricingYAML([]byte(`
chat:
  - inference_provider: anthropic
    model: claude-sonnet-4-6
    input_usd_per_million: 3
    output_usd_per_million: 15
    cached_input_usd_per_million: 0.30
    cache_creation_input_usd_per_million: 3.75
    cache_creation_input_detail_usd_per_million:
      " Ephemeral_1H_Input_Tokens ": 6
`))
	if err != nil {
		t.Fatalf("parse pricing yaml: %v", err)
	}

	cost, ok := catalog.EstimateChatCost("claude-sonnet-4-6", Usage{
		InputTokens:  80,
		OutputTokens: 10,
		TotalTokens:  90,
		Cache: UsageCache{
			CacheCreationInputTokens: 20,
			Details: map[string]int{
				"ephemeral_1h_input_tokens": 20,
			},
		},
	})
	if !ok {
		t.Fatal("expected cost estimate")
	}

	assertNearlyEqual(t, cost.Input, 60*3.0/1_000_000)
	assertNearlyEqual(t, cost.CacheCreationInput, 20*6.0/1_000_000)
	assertNearlyEqual(t, cost.Output, 10*15.0/1_000_000)
	assertNearlyEqual(t, cost.Total, 0.00045)
}

func TestPricingCatalogEstimateChatCostRejectsOverlappingCacheCreationDetails(t *testing.T) {
	catalog := &PricingCatalog{
		Chat: []ChatPricingRule{
			{
				InferenceProvider:               "anthropic",
				Model:                           "claude-sonnet-4-6",
				InputUSDPerMillion:              3,
				OutputUSDPerMillion:             15,
				CacheCreationInputUSDPerMillion: float64Ptr(3.75),
				CacheCreationInputDetailUSDPerMillion: map[string]float64{
					"ephemeral_5m_input_tokens": 3.75,
					"ephemeral_1h_input_tokens": 6,
				},
			},
		},
	}

	if cost, ok := catalog.EstimateChatCost("claude-sonnet-4-6", Usage{
		InputTokens:  40,
		OutputTokens: 10,
		TotalTokens:  50,
		Cache: UsageCache{
			CacheCreationInputTokens: 20,
			Details: map[string]int{
				"ephemeral_5m_input_tokens": 15,
				"ephemeral_1h_input_tokens": 15,
			},
		},
	}); ok || cost != nil {
		t.Fatalf("expected inconsistent cache creation details to return no cost, got %#v ok=%v", cost, ok)
	}
}

func TestPricingCatalogValidateRejectsAmbiguousModelNamesWithinInferenceProvider(t *testing.T) {
	catalog := &PricingCatalog{
		Chat: []ChatPricingRule{
			{
				InferenceProvider:   "openai",
				Model:               "gpt-5.2",
				InputUSDPerMillion:  1.25,
				OutputUSDPerMillion: 10,
			},
			{
				InferenceProvider:   "openai",
				Model:               "gpt-5.2",
				InputUSDPerMillion:  1.75,
				OutputUSDPerMillion: 14,
			},
		},
	}

	if err := catalog.Validate(); err == nil {
		t.Fatal("expected duplicate model validation error")
	}
}

func TestPricingExampleYAML(t *testing.T) {
	catalog := loadExamplePricingCatalog(t)

	mustHave := []string{
		"gpt-5.4",
		"gpt-5.4-pro",
		"gpt-5.2",
		"gpt-5",
		"gpt-4o",
		"gpt-4o-mini",
		"gpt-5.4-mini",
		"gpt-5.4-nano",
		"claude-opus-4-7",
		"claude-opus-4-5",
		"claude-opus-4-5-20250929",
		"claude-opus-4-6",
		"claude-sonnet-4-5",
		"claude-sonnet-4-6-20260201",
		"claude-sonnet-4-6",
		"claude-haiku-4-5",
		"gemini-3-pro-preview",
		"gemini-3.0-pro",
		"gemini-3-flash-preview",
		"gemini-3.0-flash",
		"gemini-2.5-pro",
		"gemini-2.5-flash",
		"mistral-large-2512",
		"mistral-large-latest",
		"mistral-medium-latest",
		"mistral-small-2603",
		"devstral-latest",
		"command-a-03-2025",
		"command-r7b-12-2024",
		"command-r-08-2024",
		"command-r-plus-08-2024",
		"glm-5",
		"glm-4.5-air",
		"kimi-k2.6",
		"kimi-k2.5",
		"kimi-k2-0905-preview",
		"MiniMax-M2.7",
		"MiniMax-M2.5-highspeed",
	}
	for _, model := range mustHave {
		if !catalogHasRule(catalog, model) {
			t.Fatalf("pricing.example.yaml missing rule %s", model)
		}
	}

	mustNotHave := []string{
		"gpt-5-pro",
		"claude-sonnet-4-20250514",
		"claude-3-7-sonnet-20250219",
		"claude-3-5-haiku-20241022",
		"gemini-3.1-flash-lite-preview",
		"gemini-2.5-flash-lite",
	}
	for _, model := range mustNotHave {
		if catalogHasRule(catalog, model) {
			t.Fatalf("pricing.example.yaml should not contain rule %s", model)
		}
	}
}

func TestPricingExampleYAMLEstimateChatCostMatchesGPT52PriceMath(t *testing.T) {
	catalog := loadExamplePricingCatalog(t)

	usage := Usage{
		InputTokens:  1000,
		OutputTokens: 300,
		TotalTokens:  1300,
		Cache: UsageCache{
			CachedInputTokens: 200,
		},
	}

	cost, ok := catalog.EstimateChatCost("gpt-5.2", usage)
	if !ok {
		t.Fatal("expected cost estimate from pricing.example.yaml")
	}

	assertNearlyEqual(t, cost.Input, 800*1.75/1_000_000)
	assertNearlyEqual(t, cost.CachedInput, 200*0.175/1_000_000)
	assertNearlyEqual(t, cost.Output, 300*14.00/1_000_000)
	assertNearlyEqual(t, cost.CacheCreationInput, 0)
	assertNearlyEqual(t, cost.Total, 0.005635)
}

func TestPricingExampleYAMLEstimateChatCostMatchesMoonshotPriceMath(t *testing.T) {
	catalog := loadExamplePricingCatalog(t)

	usage := Usage{
		InputTokens:  1000,
		OutputTokens: 300,
		TotalTokens:  1300,
		Cache: UsageCache{
			CachedInputTokens: 200,
		},
	}

	cost, ok := catalog.EstimateChatCost("kimi-k2.5", usage)
	if !ok {
		t.Fatal("expected model-based cost estimate from pricing.example.yaml")
	}

	assertNearlyEqual(t, cost.Input, 800*0.60/1_000_000)
	assertNearlyEqual(t, cost.CachedInput, 200*0.10/1_000_000)
	assertNearlyEqual(t, cost.Output, 300*3.00/1_000_000)
	assertNearlyEqual(t, cost.CacheCreationInput, 0)
	assertNearlyEqual(t, cost.Total, 0.0014)
}

func TestPricingExampleYAMLEstimateChatCostMatchesMoonshotK26PriceMath(t *testing.T) {
	catalog := loadExamplePricingCatalog(t)

	usage := Usage{
		InputTokens:  1000,
		OutputTokens: 300,
		TotalTokens:  1300,
		Cache: UsageCache{
			CachedInputTokens: 200,
		},
	}

	cost, ok := catalog.EstimateChatCost("kimi-k2.6", usage)
	if !ok {
		t.Fatal("expected model-based cost estimate from pricing.example.yaml")
	}

	assertNearlyEqual(t, cost.Input, 800*0.95/1_000_000)
	assertNearlyEqual(t, cost.CachedInput, 200*0.16/1_000_000)
	assertNearlyEqual(t, cost.Output, 300*4.00/1_000_000)
	assertNearlyEqual(t, cost.CacheCreationInput, 0)
	assertNearlyEqual(t, cost.Total, 0.001992)
}

func TestPricingExampleYAMLEstimateChatCostMatchesMistralPriceMath(t *testing.T) {
	catalog := loadExamplePricingCatalog(t)

	usage := Usage{
		InputTokens:  1000,
		OutputTokens: 300,
		TotalTokens:  1300,
	}

	cost, ok := catalog.EstimateChatCost("mistral-large-latest", usage)
	if !ok {
		t.Fatal("expected mistral alias cost estimate from pricing.example.yaml")
	}

	assertNearlyEqual(t, cost.Input, 1000*0.50/1_000_000)
	assertNearlyEqual(t, cost.CachedInput, 0)
	assertNearlyEqual(t, cost.Output, 300*1.50/1_000_000)
	assertNearlyEqual(t, cost.CacheCreationInput, 0)
	assertNearlyEqual(t, cost.Total, 0.00095)
}

func TestPricingExampleYAMLEstimateChatCostMatchesOpenAIPriceMath(t *testing.T) {
	catalog := loadExamplePricingCatalog(t)

	usage := Usage{
		InputTokens:  1000,
		OutputTokens: 300,
		TotalTokens:  1300,
		Cache: UsageCache{
			CachedInputTokens: 200,
		},
	}

	cost, ok := catalog.EstimateChatCost("gpt-5.4-mini", usage)
	if !ok {
		t.Fatal("expected cost estimate from pricing.example.yaml")
	}

	assertNearlyEqual(t, cost.Input, 800*0.75/1_000_000)
	assertNearlyEqual(t, cost.CachedInput, 200*0.075/1_000_000)
	assertNearlyEqual(t, cost.Output, 300*4.50/1_000_000)
	assertNearlyEqual(t, cost.CacheCreationInput, 0)
	assertNearlyEqual(t, cost.Total, 0.001965)
	if cost.Currency != "USD" {
		t.Fatalf("unexpected currency: %q", cost.Currency)
	}
	if !cost.Estimated {
		t.Fatal("expected estimated cost")
	}
}

func TestPricingExampleYAMLEstimateChatCostMatchesOpenAILongContextTier(t *testing.T) {
	catalog := loadExamplePricingCatalog(t)

	usage := Usage{
		InputTokens:  270001,
		OutputTokens: 300,
		TotalTokens:  270301,
		Cache: UsageCache{
			CachedInputTokens: 200,
		},
	}

	cost, ok := catalog.EstimateChatCost("gpt-5.4", usage)
	if !ok {
		t.Fatal("expected tiered cost estimate from pricing.example.yaml")
	}

	assertNearlyEqual(t, cost.Input, 269801*5.00/1_000_000)
	assertNearlyEqual(t, cost.CachedInput, 200*0.50/1_000_000)
	assertNearlyEqual(t, cost.Output, 300*22.50/1_000_000)
	assertNearlyEqual(t, cost.Total, 1.355855)
}

func TestPricingExampleYAMLEstimateChatCostMatchesGeminiLongContextTier(t *testing.T) {
	catalog := loadExamplePricingCatalog(t)

	usage := Usage{
		InputTokens:  200001,
		OutputTokens: 300,
		TotalTokens:  200301,
		Cache: UsageCache{
			CachedInputTokens: 200,
		},
	}

	cost, ok := catalog.EstimateChatCost("gemini-2.5-pro", usage)
	if !ok {
		t.Fatal("expected tiered gemini cost estimate from pricing.example.yaml")
	}

	assertNearlyEqual(t, cost.Input, 199801*2.50/1_000_000)
	assertNearlyEqual(t, cost.CachedInput, 200*0.25/1_000_000)
	assertNearlyEqual(t, cost.Output, 300*15.00/1_000_000)
	assertNearlyEqual(t, cost.Total, 0.5040525)
}

func TestPricingExampleYAMLAnnotateChatResultCostMatchesAnthropicPriceMath(t *testing.T) {
	client := New(Config{
		Provider:       "anthropic",
		AnthropicModel: "claude-sonnet-4-6",
		Pricing:        loadExamplePricingCatalog(t),
	})
	req := &chat.Request{
		Messages: []chat.Message{chat.User("hello")},
	}
	resp := &chat.Result{
		Model: "claude-sonnet-4-6",
		Usage: chat.Usage{
			InputTokens:  210,
			OutputTokens: 25,
			TotalTokens:  235,
			Cache: chat.UsageCache{
				CachedInputTokens:        80,
				CacheCreationInputTokens: 50,
				Details: map[string]int{
					"ephemeral_1h_input_tokens": 20,
				},
			},
		},
	}

	client.annotateChatResultCost("anthropic", req, resp)
	if resp.Usage.Cost == nil {
		t.Fatal("expected response usage cost from pricing.example.yaml")
	}

	assertNearlyEqual(t, resp.Usage.Cost.Input, 80*3.00/1_000_000)
	assertNearlyEqual(t, resp.Usage.Cost.CachedInput, 80*0.30/1_000_000)
	assertNearlyEqual(t, resp.Usage.Cost.CacheCreationInput, (20*6.00+30*3.75)/1_000_000)
	assertNearlyEqual(t, resp.Usage.Cost.Output, 25*15.00/1_000_000)
	assertNearlyEqual(t, resp.Usage.Cost.Total, 0.0008715)
	if resp.Usage.Cost.Currency != "USD" {
		t.Fatalf("unexpected currency: %q", resp.Usage.Cost.Currency)
	}
	if !resp.Usage.Cost.Estimated {
		t.Fatal("expected estimated cost")
	}
}

func TestWrapChatStreamCostUsesEmbeddedDefaultPricing(t *testing.T) {
	client := New(Config{
		Provider:    "openai",
		OpenAIModel: "gpt-5.4",
	})
	req := &chat.Request{
		Messages: []chat.Message{chat.User("hello")},
	}

	var got *Usage
	wrapped := client.wrapChatStreamCost("openai", req, func(ev chat.StreamEvent) error {
		got = ev.Usage
		return nil
	})

	if err := wrapped(chat.StreamEvent{
		Done: true,
		Usage: &Usage{
			InputTokens:  100,
			OutputTokens: 25,
			TotalTokens:  125,
		},
	}); err != nil {
		t.Fatalf("wrapped stream: %v", err)
	}
	if got == nil {
		t.Fatal("expected usage")
	}
	if got.Cost == nil {
		t.Fatalf("expected embedded default pricing to annotate usage, got %#v", got)
	}
	assertNearlyEqual(t, got.Cost.Total, 0.000625)
}

func TestWrapChatStreamCostEmptyCatalogDisablesDefaultPricing(t *testing.T) {
	client := New(Config{
		Provider:    "openai",
		OpenAIModel: "gpt-5.4",
		Pricing:     &PricingCatalog{},
	})
	req := &chat.Request{
		Messages: []chat.Message{chat.User("hello")},
	}

	var got *Usage
	wrapped := client.wrapChatStreamCost("openai", req, func(ev chat.StreamEvent) error {
		got = ev.Usage
		return nil
	})

	if err := wrapped(chat.StreamEvent{
		Done: true,
		Usage: &Usage{
			InputTokens:  100,
			OutputTokens: 25,
			TotalTokens:  125,
		},
	}); err != nil {
		t.Fatalf("wrapped stream: %v", err)
	}
	if got == nil {
		t.Fatal("expected usage")
	}
	if got.Cost != nil {
		t.Fatalf("expected empty catalog to disable default pricing, got %#v", got.Cost)
	}
}

func TestWrapChatStreamCostAnnotatesFinalUsage(t *testing.T) {
	client := New(Config{
		Provider:    "openai",
		OpenAIModel: "gpt-5.2",
		Pricing: &PricingCatalog{
			Chat: []ChatPricingRule{
				{
					InferenceProvider:        "openai",
					Model:                    "gpt-5.2",
					InputUSDPerMillion:       1.25,
					OutputUSDPerMillion:      10,
					CachedInputUSDPerMillion: float64Ptr(0.125),
				},
			},
		},
	})
	req := &chat.Request{
		Messages: []chat.Message{chat.User("hello")},
	}

	var got *Usage
	wrapped := client.wrapChatStreamCost("openai", req, func(ev chat.StreamEvent) error {
		got = ev.Usage
		return nil
	})

	if err := wrapped(chat.StreamEvent{
		Done: true,
		Usage: &Usage{
			InputTokens:  100,
			OutputTokens: 25,
			TotalTokens:  125,
		},
	}); err != nil {
		t.Fatalf("wrapped stream: %v", err)
	}
	if got == nil || got.Cost == nil {
		t.Fatalf("expected stream usage cost, got %#v", got)
	}
	assertNearlyEqual(t, got.Cost.Total, 0.000375)
}

func TestAnnotateChatResultCost(t *testing.T) {
	client := New(Config{
		Provider:    "openai",
		OpenAIModel: "gpt-5.2",
		Pricing: &PricingCatalog{
			Chat: []ChatPricingRule{
				{
					InferenceProvider:        "openai",
					Model:                    "gpt-5.2",
					InputUSDPerMillion:       1.25,
					OutputUSDPerMillion:      10,
					CachedInputUSDPerMillion: float64Ptr(0.125),
				},
			},
		},
	})
	req := &chat.Request{
		Messages: []chat.Message{chat.User("hello")},
	}
	resp := &chat.Result{
		Model: "gpt-5.2",
		Usage: chat.Usage{
			InputTokens:  100,
			OutputTokens: 25,
			TotalTokens:  125,
		},
	}

	client.annotateChatResultCost("openai", req, resp)
	if resp.Usage.Cost == nil {
		t.Fatal("expected response usage cost")
	}
	assertNearlyEqual(t, resp.Usage.Cost.Total, 0.000375)
}

func TestAnnotateChatResultCostUsesRequestInferenceProvider(t *testing.T) {
	client := New(Config{
		Provider:    "openai",
		OpenAIModel: "gpt-5",
		Pricing: &PricingCatalog{
			Chat: []ChatPricingRule{
				{
					InferenceProvider:   "openai",
					Model:               "gpt-5",
					InputUSDPerMillion:  1.25,
					OutputUSDPerMillion: 10,
				},
				{
					InferenceProvider:   "azure",
					Model:               "gpt-5",
					InputUSDPerMillion:  2.00,
					OutputUSDPerMillion: 12,
				},
			},
		},
	})
	req := &chat.Request{
		Model:             "gpt-5",
		InferenceProvider: "azure",
		Messages:          []chat.Message{chat.User("hello")},
	}
	resp := &chat.Result{
		Model: "gpt-5",
		Usage: chat.Usage{
			InputTokens:  100,
			OutputTokens: 25,
			TotalTokens:  125,
		},
	}

	client.annotateChatResultCost("openai", req, resp)
	if resp.Usage.Cost == nil {
		t.Fatal("expected response usage cost")
	}
	assertNearlyEqual(t, resp.Usage.Cost.Input, 0.0002)
	assertNearlyEqual(t, resp.Usage.Cost.Output, 0.0003)
	assertNearlyEqual(t, resp.Usage.Cost.Total, 0.0005)
}

func TestAnnotateChatResultCostIgnoresDriverProvider(t *testing.T) {
	client := New(Config{
		Provider:    "openai_resp",
		OpenAIModel: "gpt-5.2",
		Pricing: &PricingCatalog{
			Chat: []ChatPricingRule{
				{
					InferenceProvider:        "openai",
					Model:                    "gpt-5.2",
					InputUSDPerMillion:       1.25,
					OutputUSDPerMillion:      10,
					CachedInputUSDPerMillion: float64Ptr(0.125),
				},
			},
		},
	})
	req := &chat.Request{
		Messages: []chat.Message{chat.User("hello")},
	}
	resp := &chat.Result{
		Model: "gpt-5.2",
		Usage: chat.Usage{
			InputTokens:  100,
			OutputTokens: 25,
			TotalTokens:  125,
		},
	}

	client.annotateChatResultCost("openai_resp", req, resp)
	if resp.Usage.Cost == nil {
		t.Fatal("expected response usage cost")
	}
	assertNearlyEqual(t, resp.Usage.Cost.Total, 0.000375)
}

func TestWrapChatStreamCostUsesRequestInferenceProvider(t *testing.T) {
	client := New(Config{
		Provider:    "openai",
		OpenAIModel: "gpt-5",
		Pricing: &PricingCatalog{
			Chat: []ChatPricingRule{
				{
					InferenceProvider:   "openai",
					Model:               "gpt-5",
					InputUSDPerMillion:  1.25,
					OutputUSDPerMillion: 10,
				},
				{
					InferenceProvider:   "azure",
					Model:               "gpt-5",
					InputUSDPerMillion:  2.00,
					OutputUSDPerMillion: 12,
				},
			},
		},
	})
	req := &chat.Request{
		Model:             "gpt-5",
		InferenceProvider: "azure",
		Messages:          []chat.Message{chat.User("hello")},
	}

	var got *Usage
	wrapped := client.wrapChatStreamCost("openai", req, func(ev chat.StreamEvent) error {
		got = ev.Usage
		return nil
	})

	if err := wrapped(chat.StreamEvent{
		Done: true,
		Usage: &Usage{
			InputTokens:  100,
			OutputTokens: 25,
			TotalTokens:  125,
		},
	}); err != nil {
		t.Fatalf("wrapped stream: %v", err)
	}
	if got == nil || got.Cost == nil {
		t.Fatalf("expected priced final usage, got %#v", got)
	}
	assertNearlyEqual(t, got.Cost.Input, 0.0002)
	assertNearlyEqual(t, got.Cost.Output, 0.0003)
	assertNearlyEqual(t, got.Cost.Total, 0.0005)
}

func TestAnnotateChatResultCostWithoutUsageDoesNotAnnotate(t *testing.T) {
	client := New(Config{
		Provider:    "openai",
		OpenAIModel: "gpt-5.2",
		Pricing: &PricingCatalog{
			Chat: []ChatPricingRule{
				{
					InferenceProvider:   "openai",
					Model:               "gpt-5.2",
					InputUSDPerMillion:  1.25,
					OutputUSDPerMillion: 10,
				},
			},
		},
	})
	req := &chat.Request{
		Messages: []chat.Message{chat.User("hello")},
	}
	resp := &chat.Result{
		Model: "gpt-5.2",
	}

	client.annotateChatResultCost("openai", req, resp)
	if resp.Usage.Cost != nil {
		t.Fatalf("expected no response usage cost for empty usage, got %#v", resp.Usage.Cost)
	}
}

func assertNearlyEqual(t *testing.T, got, want float64) {
	t.Helper()
	const epsilon = 1e-12
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	if diff > epsilon {
		t.Fatalf("got %.12f want %.12f", got, want)
	}
}

func float64Ptr(v float64) *float64 {
	return &v
}

func intPtr(v int) *int {
	return &v
}

func loadExamplePricingCatalog(t *testing.T) *PricingCatalog {
	t.Helper()

	data, err := os.ReadFile("pricing.example.yaml")
	if err != nil {
		t.Fatalf("read pricing.example.yaml: %v", err)
	}

	catalog, err := ParsePricingYAML(data)
	if err != nil {
		t.Fatalf("parse pricing.example.yaml: %v", err)
	}
	return catalog
}

func catalogHasRule(catalog *PricingCatalog, model string) bool {
	if catalog == nil {
		return false
	}
	return catalog.findChatPricingRule(model) != nil
}
