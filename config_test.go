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

func TestConfigSakanaUsesBuiltInAPIBase(t *testing.T) {
	client := New(Config{
		Provider:    "sakana",
		OpenAIModel: "fugu-ultra",
	})

	cfg := client.GetConfig()
	if cfg.APIBase != sakanaAPIBase {
		t.Fatalf("Sakana API base = %q, want %q", cfg.APIBase, sakanaAPIBase)
	}
	if cfg.Model != "fugu-ultra" {
		t.Fatalf("Sakana model = %q", cfg.Model)
	}
}
