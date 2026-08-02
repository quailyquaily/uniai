package modelcompat

import "strings"

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

func KimiUsesFixedSampling(model string) bool {
	model = Normalize(model)
	return modelHasPrefix(model, "kimi-k3") ||
		modelHasPrefix(model, "kimi-k2-6") ||
		modelHasPrefix(model, "kimi-k2-5")
}

func OpenAIChatCompletionReasoningDetailsSupported(provider, model string) bool {
	if strings.EqualFold(strings.TrimSpace(provider), "deepseek") {
		return true
	}
	model = Normalize(model)
	return modelHasPrefix(model, "deepseek") || modelHasPrefix(model, "kimi")
}

func AnthropicDropsSamplingParameters(model string) bool {
	model = strings.ToLower(model)
	return strings.Contains(model, "fable-5") ||
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
	model = strings.ToLower(model)
	return strings.Contains(model, "fable-5") ||
		strings.Contains(model, "mythos-5") ||
		strings.Contains(model, "opus-5") ||
		strings.Contains(model, "sonnet-5") ||
		strings.Contains(model, "opus-4-8") ||
		strings.Contains(model, "opus-4-7") ||
		strings.Contains(model, "opus-4-6") ||
		strings.Contains(model, "sonnet-4-6")
}

func AnthropicSummarizesThinkingDetails(model string) bool {
	model = strings.ToLower(model)
	return strings.Contains(model, "opus-5") || strings.Contains(model, "opus-4-7")
}

func NormalizeKimiReasoningEffort(model, effort string) (string, bool) {
	if !modelHasPrefix(Normalize(model), "kimi-k3") {
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

func OpenAIGPT5DropsSampling(model, reasoningEffort string, reasoningRequested bool) bool {
	model = Normalize(model)
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

func OpenAIUsesPromptCacheOptions(model string) bool {
	model = Normalize(model)
	return modelHasPrefix(model, "gpt-5-6")
}

func OpenAISupportsPromptCacheBreakpoints(model string) bool {
	model = Normalize(model)
	return modelHasPrefix(model, "gpt-5-6") &&
		!modelHasPrefix(model, "gpt-5-6-luna")
}

func OpenAIReasoningEffortSupported(model, effort string) bool {
	if !OpenAIUsesPromptCacheOptions(model) {
		return true
	}
	switch effort {
	case "", "none", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

func OpenAIReasoningModeSupported(model, mode string) bool {
	if !OpenAIUsesPromptCacheOptions(model) {
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
	if !OpenAIUsesPromptCacheOptions(model) {
		return true
	}
	switch context {
	case "", "auto", "current_turn", "all_turns":
		return true
	default:
		return false
	}
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
