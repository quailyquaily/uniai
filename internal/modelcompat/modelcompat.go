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

func KimiK2UsesFixedSampling(model string) bool {
	model = Normalize(model)
	return modelHasPrefix(model, "kimi-k2-6") || modelHasPrefix(model, "kimi-k2-5")
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
