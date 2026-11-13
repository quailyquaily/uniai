package modelcompat

import (
	"net/url"
	"strings"
)

func Normalize(model string) string {
	model = strings.TrimSpace(strings.ToLower(model))
	model = strings.TrimPrefix(model, "models/")
	if idx := strings.LastIndex(model, "/"); idx >= 0 {
		model = model[idx+1:]
	}
	if !strings.Contains(model, ".") {
		return model
	}
	var b strings.Builder
	b.Grow(len(model))
	for i := 0; i < len(model); i++ {
		ch := model[i]
		if ch == '.' && i > 0 && i+1 < len(model) && isASCIIDigit(model[i-1]) && isASCIIDigit(model[i+1]) {
			b.WriteByte('-')
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}

func IsKimiK3(model string) bool {
	return modelHasPrefix(Normalize(model), "kimi-k3")
}

func KimiUsesFixedSampling(model string) bool {
	model = Normalize(model)
	return IsKimiK3(model) ||
		modelHasPrefix(model, "kimi-k2-6") ||
		modelHasPrefix(model, "kimi-k2-5")
}

// IsGemini4 reports whether model is a Gemini 4 model with confirmed API rules.
// Rules were checked on 2026-11-13 against https://aistudio.google.com/docs/thinking
// and the per-model "What's new" pages. Unlisted Gemini 4 IDs are not covered.
func IsGemini4(model string) bool {
	model = Normalize(model)
	return modelHasPrefix(model, "gemini-4-carbon") ||
		modelHasPrefix(model, "gemini-4-argon") ||
		modelHasPrefix(model, "gemini-4-hydrogen")
}

// Gemini4ThinkingLevelSupported reports whether a native generateContent
// thinkingLevel is accepted. Gemini 4 models cannot disable thinking and
// reject minimal; only Hydrogen accepts xhigh.
func Gemini4ThinkingLevelSupported(model, level string) bool {
	switch level {
	case "low", "medium", "high":
		return true
	case "xhigh":
		return modelHasPrefix(Normalize(model), "gemini-4-hydrogen")
	default:
		return false
	}
}

// Gemini4OpenAIReasoningEffortSupported reports whether the Gemini
// OpenAI-compatible endpoint accepts reasoning_effort for a Gemini 4 model.
// Upstream maps minimal to low there; none is not supported.
func Gemini4OpenAIReasoningEffortSupported(model, effort string) bool {
	if effort == "" || effort == "minimal" {
		return true
	}
	return Gemini4ThinkingLevelSupported(model, effort)
}

func DeepSeekUsesThinkingMode(model string) bool {
	model = Normalize(model)
	return modelHasPrefix(model, "deepseek-flash") ||
		modelHasPrefix(model, "deepseek-v4-flash") || modelHasPrefix(model, "deepseek-v4-pro")
}

func AnthropicDropsSamplingParameters(model string) bool {
	model = Normalize(model)
	return strings.Contains(model, "fable-5") ||
		strings.Contains(model, "haiku-5-5") ||
		strings.Contains(model, "mythos-5") ||
		strings.Contains(model, "opus-5") ||
		strings.Contains(model, "sonnet-5") ||
		strings.Contains(model, "opus-4-8") ||
		strings.Contains(model, "opus-4-7")
}

func AnthropicSupportsReasoningEffort(model string) bool {
	model = strings.ToLower(model)
	return strings.Contains(model, "opus-4-5") || AnthropicPrefersReasoningEffort(model)
}

func AnthropicPrefersReasoningEffort(model string) bool {
	model = Normalize(model)
	return strings.Contains(model, "fable-5") ||
		strings.Contains(model, "haiku-5-5") ||
		strings.Contains(model, "mythos-5") ||
		strings.Contains(model, "opus-5") ||
		strings.Contains(model, "sonnet-5") ||
		strings.Contains(model, "opus-4-8") ||
		strings.Contains(model, "opus-4-7") ||
		strings.Contains(model, "opus-4-6") ||
		strings.Contains(model, "sonnet-4-6")
}

func AnthropicSummarizesThinkingDetails(model string) bool {
	model = Normalize(model)
	return strings.Contains(model, "opus-5") || strings.Contains(model, "opus-4-7") ||
		strings.Contains(model, "haiku-5-5") ||
		strings.Contains(model, "fable-5") || strings.Contains(model, "mythos-5") ||
		AnthropicSupportsBetweenTools(model)
}

func AnthropicSupportsBetweenTools(model string) bool {
	model = Normalize(model)
	// Bedrock IDs may include an inference profile and the anthropic namespace.
	if idx := strings.LastIndex(model, "."); idx >= 0 {
		model = model[idx+1:]
	}
	return modelHasPrefix(model, "claude-sonnet-5-5")
}

func AnthropicRejectsForcedToolChoice(model string) bool {
	model = Normalize(model)
	return modelHasPrefix(model, "claude-fable-5-1") || modelHasPrefix(model, "claude-mythos-5-1") ||
		modelHasPrefix(model, "claude-opus-5-5") || AnthropicSupportsBetweenTools(model)
}

func AnthropicReasoningEffortSupported(model, effort string) bool {
	if !strings.Contains(Normalize(model), "claude-haiku-5-5") && !modelHasPrefix(Normalize(model), "claude-opus-5-5") && !AnthropicSupportsBetweenTools(model) {
		return true
	}
	switch effort {
	case "", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

// AnthropicThinkingTypeSupported validates explicit thinking modes across
// the native API and Bedrock inference profile IDs.
func AnthropicThinkingTypeSupported(model, mode string) bool {
	if strings.Contains(Normalize(model), "claude-haiku-5-5") {
		return mode == "adaptive" || mode == "disabled"
	}
	return AnthropicSupportsBetweenTools(model) && (mode == "adaptive" || mode == "between_tools")
}

func NormalizeKimiReasoningEffort(model, effort string) (string, bool) {
	if !IsKimiK3(model) {
		return effort, true
	}

	effort = strings.TrimSpace(strings.ToLower(effort))
	switch effort {
	case "", "low", "high", "max":
		return effort, true
	case "xhigh":
		return "max", true
	default:
		return effort, false
	}
}

func OpenAIDropsSampling(model, reasoningEffort string, reasoningRequested bool) bool {
	model = Normalize(model)
	if IsGPT6(model) {
		return IsGPT6Astra(model) || strings.TrimSpace(strings.ToLower(reasoningEffort)) != "none"
	}
	if !strings.HasPrefix(model, "gpt-5") {
		return false
	}
	if modelHasPrefix(model, "gpt-5-5") {
		return true
	}
	if openAIGPT5AllowsSamplingWithNoReasoning(model) {
		effort := strings.TrimSpace(strings.ToLower(reasoningEffort))
		return reasoningRequested && effort != "none"
	}
	return true
}

func OpenAIRequires24hPromptCacheRetention(model string) bool {
	model = Normalize(model)
	return modelHasPrefix(model, "gpt-5-5")
}

// OpenAIUsesPromptCacheOptions reports whether model uses
// prompt_cache_options.ttl and explicit prompt_cache_breakpoint markers instead
// of the legacy prompt_cache_retention field. Keep in sync with docs/cache.md.
func OpenAIUsesPromptCacheOptions(model string) bool {
	model = Normalize(model)
	return openAIUsesGPT6Parameters(model) || modelHasPrefix(model, "gpt-6-1-sol")
}

// OpenAIRejectsPromptCacheOptions reports whether model is known to use only the
// legacy prompt_cache_retention field. Unknown models are not rejected.
func OpenAIRejectsPromptCacheOptions(model string) bool {
	model = Normalize(model)
	return modelHasPrefix(model, "gpt-5-4") || modelHasPrefix(model, "gpt-5-5")
}

func IsGPT6(model string) bool {
	model = Normalize(model)
	return modelHasPrefix(model, "gpt-6-astra") ||
		modelHasPrefix(model, "gpt-6-1-terra") ||
		modelHasPrefix(model, "gpt-6-sol") || modelHasPrefix(model, "gpt-6-luna")
}

func OpenAIRequiresResponsesForTools(model string) bool {
	return IsGPT6Astra(model) || modelHasPrefix(Normalize(model), "gpt-6-1-terra")
}

// IsOfficialOpenAIBase reports whether a Chat Completions base URL points at
// OpenAI's own API. An empty base means the SDK default, which is official.
// The Responses-routing rules for GPT-6 (tool calling must go through
// /v1/responses) describe api.openai.com; an OpenAI-compatible gateway serving
// GPT-6 model IDs over Chat Completions handles that translation itself, so
// those rules are not applied to it.
func IsOfficialOpenAIBase(base string) bool {
	base = strings.TrimSpace(base)
	if base == "" {
		return true
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Hostname(), "api.openai.com")
}

func IsGPT6Astra(model string) bool {
	return modelHasPrefix(Normalize(model), "gpt-6-astra")
}

func OpenAIReasoningEffortSupported(model, effort string) bool {
	if IsGemini4(model) {
		return Gemini4OpenAIReasoningEffortSupported(model, effort)
	}
	if !openAIUsesGPT6Parameters(Normalize(model)) {
		return true
	}
	switch effort {
	case "none":
		return !IsGPT6Astra(model)
	case "", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

func OpenAIReasoningModeSupported(model, mode string) bool {
	if !openAIUsesGPT6Parameters(Normalize(model)) {
		return true
	}
	switch mode {
	case "", "standard", "pro":
		return true
	default:
		return false
	}
}

func OpenAIReasoningContextSupported(model, context string) bool {
	if !openAIUsesGPT6Parameters(Normalize(model)) {
		return true
	}
	switch context {
	case "", "auto", "current_turn", "all_turns":
		return true
	default:
		return false
	}
}

// openAIUsesGPT6Parameters reports whether model uses the GPT-5.6/GPT-6
// reasoning parameter set. GPT-6.1 Sol is not included until its rules are
// confirmed separately from its cache policy.
func openAIUsesGPT6Parameters(model string) bool {
	return modelHasPrefix(model, "gpt-5-6") || IsGPT6(model)
}

func openAIGPT5AllowsSamplingWithNoReasoning(model string) bool {
	return modelHasPrefix(model, "gpt-5-1") ||
		modelHasPrefix(model, "gpt-5-2") ||
		modelHasPrefix(model, "gpt-5-4")
}

func modelHasPrefix(model, prefix string) bool {
	return model == prefix || strings.HasPrefix(model, prefix+"-")
}

func isASCIIDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}
