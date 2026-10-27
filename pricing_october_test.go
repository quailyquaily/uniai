package uniai

import "testing"

func TestOctoberCacheWritePricing(t *testing.T) {
	catalog := DefaultPricingCatalog()
	for _, tt := range []struct {
		model               string
		input               int
		cached, write, hour float64
	}{
		{"gpt-6.1-terra", 272000, 0.05, 1.25, 1.25},
		{"gpt-6.1-terra", 272001, 0.10, 2.50, 2.50},
		{"claude-haiku-5-5", 100000, 0.01, 0.125, 0.20},
		{"claude-haiku-5-5", 100001, 0.05, 0.625, 1.00},
		{"kimi-k3.1-pro", 1000, 0.20, 2, 2},
		{"kimi-k3.1-pro-highspeed", 1000, 0.20, 4, 4},
		{"kimi-k3", 1000, 0.30, 3, 3},
	} {
		usage := Usage{InputTokens: tt.input, Cache: UsageCache{CachedInputTokens: 200, CacheCreationInputTokens: 100,
			Details: map[string]int{"ephemeral_1h_input_tokens": 40}}}
		cost, ok := catalog.EstimateChatCost(tt.model, usage)
		if !ok {
			t.Fatalf("%s: missing price", tt.model)
		}
		assertNearlyEqual(t, cost.CachedInput, 200*tt.cached/1e6)
		assertNearlyEqual(t, cost.CacheCreationInput, (60*tt.write+40*tt.hour)/1e6)
	}
}
