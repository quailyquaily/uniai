package main

import "testing"

func TestSelectedProvidersReadsOpenAIAPIBase(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_API_BASE", "  https://images.example.test/v1/  ")

	providers, err := selectedProviders("openai", "gpt-image-2", "")
	if err != nil {
		t.Fatalf("selectedProviders() error = %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("len(providers) = %d, want 1", len(providers))
	}
	if providers[0].apiBase != "https://images.example.test/v1/" {
		t.Fatalf("apiBase = %q", providers[0].apiBase)
	}
}
