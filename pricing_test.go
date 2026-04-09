package uniai

import (
	"os"
	"testing"

	"github.com/quailyquaily/uniai/chat"
)

func TestParsePricingYAML(t *testing.T) {
	catalog, err := ParsePricingYAML([]byte(`
version: uniai.pricing.v1
chat:
  - provider: openai
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
	if catalog.Version != PricingCatalogVersionV1 {
		t.Fatalf("unexpected version: %q", catalog.Version)
	}
	if len(catalog.Chat) != 1 {
		t.Fatalf("unexpected chat rule count: %d", len(catalog.Chat))
	}
	rule := catalog.Chat[0]
	if rule.Provider != "openai" || rule.Model != "gpt-5.2" {
		t.Fatalf("unexpected rule: %#v", rule)
	}
	if len(rule.Aliases) != 1 || rule.Aliases[0] != "gpt-5.2-20260401" {
		t.Fatalf("unexpected aliases: %#v", rule.Aliases)
	}
	if rule.CachedInputUSDPerMillion == nil || *rule.CachedInputUSDPerMillion != 0.125 {
		t.Fatalf("unexpected cached input price: %#v", rule.CachedInputUSDPerMillion)
	}
}

func TestPricingCatalogEstimateChatCost(t *testing.T) {
	catalog := &PricingCatalog{
		Chat: []ChatPricingRule{
			{
				Provider:                 "openai",
				Model:                    "gpt-5.2",
				InputUSDPerMillion:       1.25,
				OutputUSDPerMillion:      10,
				CachedInputUSDPerMillion: float64Ptr(0.125),
				Aliases:                  []string{"gpt-5.2-20260401"},
			},
		},
	}

	cost, ok := catalog.EstimateChatCost("openai", "gpt-5.2-20260401", Usage{
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

func TestPricingCatalogEstimateChatCostAnthropicCacheCreationDetails(t *testing.T) {
	catalog := &PricingCatalog{
		Chat: []ChatPricingRule{
			{
				Provider:                        "anthropic",
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

	cost, ok := catalog.EstimateChatCost("anthropic", "claude-sonnet-4-20250514", Usage{
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
	if _, ok := catalog.EstimateChatCost("openai", "gpt-5.2", Usage{
		InputTokens:  10,
		OutputTokens: 5,
		TotalTokens:  15,
	}); ok {
		t.Fatal("expected empty catalog to produce no cost")
	}
}

func TestPricingCatalogEstimateChatCostWithoutUsageReturnsNoCost(t *testing.T) {
	catalog := &PricingCatalog{
		Chat: []ChatPricingRule{
			{
				Provider:            "openai",
				Model:               "gpt-5.2",
				InputUSDPerMillion:  1.25,
				OutputUSDPerMillion: 10,
			},
		},
	}

	if cost, ok := catalog.EstimateChatCost("openai", "gpt-5.2", Usage{}); ok || cost != nil {
		t.Fatalf("expected no cost for empty usage, got %#v ok=%v", cost, ok)
	}
}

func TestPricingCatalogEstimateChatCostNormalizesDetailRateKeysAtLookup(t *testing.T) {
	catalog, err := ParsePricingYAML([]byte(`
version: uniai.pricing.v1
chat:
  - provider: anthropic
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

	cost, ok := catalog.EstimateChatCost("anthropic", "claude-sonnet-4-6", Usage{
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

func TestPricingExampleYAML(t *testing.T) {
	catalog := loadExamplePricingCatalog(t)

	mustHave := []struct {
		provider string
		model    string
	}{
		{provider: "openai", model: "gpt-5.4"},
		{provider: "openai_resp", model: "gpt-5.4-mini"},
		{provider: "anthropic", model: "claude-opus-4-6"},
		{provider: "anthropic", model: "claude-sonnet-4-6"},
		{provider: "gemini", model: "gemini-3.1-pro-preview"},
		{provider: "gemini", model: "gemini-3-flash-preview"},
	}
	for _, tc := range mustHave {
		if !catalogHasRule(catalog, tc.provider, tc.model) {
			t.Fatalf("pricing.example.yaml missing rule %s/%s", tc.provider, tc.model)
		}
	}

	mustNotHave := []struct {
		provider string
		model    string
	}{
		{provider: "openai", model: "gpt-5-pro"},
		{provider: "openai_resp", model: "gpt-5-pro"},
		{provider: "anthropic", model: "claude-sonnet-4-20250514"},
		{provider: "anthropic", model: "claude-3-7-sonnet-20250219"},
		{provider: "anthropic", model: "claude-3-5-haiku-20241022"},
		{provider: "gemini", model: "gemini-2.5-pro"},
		{provider: "gemini", model: "gemini-2.5-flash"},
		{provider: "gemini", model: "gemini-3.1-flash-lite-preview"},
	}
	for _, tc := range mustNotHave {
		if catalogHasRule(catalog, tc.provider, tc.model) {
			t.Fatalf("pricing.example.yaml should not contain rule %s/%s", tc.provider, tc.model)
		}
	}
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

	cost, ok := catalog.EstimateChatCost("openai", "gpt-5.4-mini", usage)
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

func TestWrapChatStreamCostWithoutPricingDoesNotAnnotate(t *testing.T) {
	client := New(Config{
		Provider:    "openai",
		OpenAIModel: "gpt-5.2",
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
		t.Fatalf("expected no cost without pricing, got %#v", got.Cost)
	}
}

func TestWrapChatStreamCostAnnotatesFinalUsage(t *testing.T) {
	client := New(Config{
		Provider:    "openai",
		OpenAIModel: "gpt-5.2",
		Pricing: &PricingCatalog{
			Chat: []ChatPricingRule{
				{
					Provider:                 "openai",
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
					Provider:                 "openai",
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

func TestAnnotateChatResultCostWithoutUsageDoesNotAnnotate(t *testing.T) {
	client := New(Config{
		Provider:    "openai",
		OpenAIModel: "gpt-5.2",
		Pricing: &PricingCatalog{
			Chat: []ChatPricingRule{
				{
					Provider:            "openai",
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

func catalogHasRule(catalog *PricingCatalog, provider, model string) bool {
	if catalog == nil {
		return false
	}
	for _, rule := range catalog.Chat {
		if normalizeProvider(rule.Provider) != normalizeProvider(provider) {
			continue
		}
		if normalizeModel(rule.Model) == normalizeModel(model) {
			return true
		}
	}
	return false
}
