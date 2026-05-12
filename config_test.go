package uniai

import "testing"

func TestConfigDefaultsIncludeAnthropicAPIBase(t *testing.T) {
	client := New(Config{Provider: "anthropic"})

	cfg := client.GetConfig()
	if cfg.APIBase != DefaultAnthropicAPIBase {
		t.Fatalf("Anthropic API base = %q, want %q", cfg.APIBase, DefaultAnthropicAPIBase)
	}
}

func TestConfigPreservesCustomAnthropicAPIBase(t *testing.T) {
	client := New(Config{
		Provider:         "anthropic",
		AnthropicAPIBase: "https://example.test/anthropic/v1",
	})

	cfg := client.GetConfig()
	if cfg.APIBase != "https://example.test/anthropic/v1" {
		t.Fatalf("Anthropic API base = %q", cfg.APIBase)
	}
}
