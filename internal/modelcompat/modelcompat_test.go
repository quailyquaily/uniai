package modelcompat

import "testing"

func TestNormalize(t *testing.T) {
	got := Normalize("openai/GPT-5.4")
	if got != "gpt-5-4" {
		t.Fatalf("unexpected normalized model: %q", got)
	}
}

func TestKimiK2UsesFixedSampling(t *testing.T) {
	if !KimiK2UsesFixedSampling("moonshotai/kimi-k2.6") {
		t.Fatalf("expected kimi-k2.6 to use fixed sampling")
	}
	if KimiK2UsesFixedSampling("kimi-k2-0905-preview") {
		t.Fatalf("expected kimi-k2-0905-preview not to match K2.5/K2.6 fixed sampling")
	}
}

func TestOpenAIGPT5DropsSampling(t *testing.T) {
	if !OpenAIGPT5DropsSampling("gpt-5.2", "high", true) {
		t.Fatalf("expected gpt-5.2 with reasoning to drop sampling")
	}
	if OpenAIGPT5DropsSampling("gpt-5.2", "none", true) {
		t.Fatalf("expected gpt-5.2 with reasoning none to keep sampling")
	}
	if !OpenAIGPT5DropsSampling("gpt-5.5", "none", true) {
		t.Fatalf("expected gpt-5.5 to drop sampling")
	}
	if !OpenAIGPT5DropsSampling("gpt-5", "", false) {
		t.Fatalf("expected older gpt-5 to drop sampling")
	}
	if OpenAIGPT5DropsSampling("gpt-4.1", "high", true) {
		t.Fatalf("expected gpt-4.1 not to match GPT-5 sampling rules")
	}
}
