package modelcompat

import "testing"

func TestNormalize(t *testing.T) {
	got := Normalize("openai/GPT-5.4")
	if got != "gpt-5-4" {
		t.Fatalf("unexpected normalized model: %q", got)
	}
}

func TestKimiUsesFixedSampling(t *testing.T) {
	if !KimiUsesFixedSampling("moonshotai/kimi-k2.6") {
		t.Fatalf("expected kimi-k2.6 to use fixed sampling")
	}
	if !KimiUsesFixedSampling("kimi-k3") {
		t.Fatalf("expected kimi-k3 to use fixed sampling")
	}
	if KimiUsesFixedSampling("kimi-k2-0905-preview") {
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

func TestOpenAIRequires24hPromptCacheRetention(t *testing.T) {
	if !OpenAIRequires24hPromptCacheRetention("openai/gpt-5.5") {
		t.Fatalf("expected gpt-5.5 to require 24h prompt cache retention")
	}
	if OpenAIRequires24hPromptCacheRetention("gpt-5.4") {
		t.Fatalf("expected gpt-5.4 not to require 24h prompt cache retention")
	}
}

func TestOpenAIUsesPromptCacheOptions(t *testing.T) {
	for _, model := range []string{
		"gpt-5.6",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"openai/gpt-5.6-luna",
	} {
		if !OpenAIUsesPromptCacheOptions(model) {
			t.Fatalf("expected %q to use prompt_cache_options", model)
		}
	}
	if OpenAIUsesPromptCacheOptions("gpt-5.5") {
		t.Fatalf("expected gpt-5.5 to keep using prompt_cache_retention")
	}
}

func TestOpenAIReasoningEffortSupported(t *testing.T) {
	for _, effort := range []string{"none", "low", "medium", "high", "xhigh", "max"} {
		if !OpenAIReasoningEffortSupported("gpt-5.6", effort) {
			t.Fatalf("expected GPT-5.6 reasoning effort %q to be supported", effort)
		}
	}
	for _, effort := range []string{"minimal", "unknown", "HIGH"} {
		if OpenAIReasoningEffortSupported("gpt-5.6-sol", effort) {
			t.Fatalf("expected GPT-5.6 reasoning effort %q to be rejected", effort)
		}
	}
	if !OpenAIReasoningEffortSupported("gpt-5", "minimal") {
		t.Fatalf("expected legacy GPT-5 minimal reasoning effort to remain supported")
	}
}

func TestOpenAIReasoningModeSupported(t *testing.T) {
	for _, mode := range []string{"", "standard", "pro"} {
		if !OpenAIReasoningModeSupported("gpt-5.6", mode) {
			t.Fatalf("expected GPT-5.6 reasoning mode %q to be supported", mode)
		}
	}
	for _, mode := range []string{"automatic", "PRO"} {
		if OpenAIReasoningModeSupported("gpt-5.6", mode) {
			t.Fatalf("expected GPT-5.6 reasoning mode %q to be rejected", mode)
		}
	}
}

func TestOpenAIReasoningContextSupported(t *testing.T) {
	for _, context := range []string{"", "auto", "current_turn", "all_turns"} {
		if !OpenAIReasoningContextSupported("gpt-5.6", context) {
			t.Fatalf("expected GPT-5.6 reasoning context %q to be supported", context)
		}
	}
	for _, context := range []string{"conversation", "ALL_TURNS"} {
		if OpenAIReasoningContextSupported("gpt-5.6", context) {
			t.Fatalf("expected GPT-5.6 reasoning context %q to be rejected", context)
		}
	}
}
