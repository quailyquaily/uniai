package chat

import (
	"fmt"
	"strings"
)

func CloneCacheControl(ctrl *CacheControl) *CacheControl {
	if ctrl == nil {
		return nil
	}
	out := *ctrl
	return &out
}

func CloneParts(parts []Part) []Part {
	if len(parts) == 0 {
		return nil
	}
	out := make([]Part, len(parts))
	for i := range parts {
		out[i] = parts[i]
		out[i].CacheControl = CloneCacheControl(parts[i].CacheControl)
	}
	return out
}

func CloneTools(tools []Tool) []Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]Tool, len(tools))
	for i := range tools {
		out[i] = tools[i]
		out[i].CacheControl = CloneCacheControl(tools[i].CacheControl)
	}
	return out
}

func NormalizeMessageParts(msg Message) []Part {
	if len(msg.Parts) > 0 {
		return CloneParts(msg.Parts)
	}
	if msg.Content == "" {
		return nil
	}
	return []Part{{Type: PartTypeText, Text: msg.Content}}
}

func ValidateMessageParts(msg Message) error {
	for i, part := range msg.Parts {
		if err := ValidatePart(part); err != nil {
			return fmt.Errorf("part[%d]: %w", i, err)
		}
	}
	return nil
}

func ValidatePart(part Part) error {
	if err := ValidateCacheControl(part.CacheControl); err != nil {
		return err
	}
	switch part.Type {
	case PartTypeText:
		if part.CacheControl != nil && strings.TrimSpace(part.Text) == "" {
			return fmt.Errorf("cache control requires non-empty text part")
		}
		return nil
	case PartTypeImageURL:
		if strings.TrimSpace(part.URL) == "" {
			return fmt.Errorf("part type %q requires url", PartTypeImageURL)
		}
		return nil
	case PartTypeImageBase64:
		if strings.TrimSpace(part.DataBase64) == "" {
			return fmt.Errorf("part type %q requires data_base64", PartTypeImageBase64)
		}
		return nil
	default:
		return fmt.Errorf("unsupported part type %q", part.Type)
	}
}

func ValidateCacheControl(ctrl *CacheControl) error {
	if ctrl == nil {
		return nil
	}
	switch strings.TrimSpace(ctrl.TTL) {
	case "", "5m", "1h":
		return nil
	default:
		return fmt.Errorf("unsupported cache ttl %q", ctrl.TTL)
	}
}

func RequestHasExplicitCacheControl(req *Request) bool {
	if req == nil {
		return false
	}
	for _, msg := range req.Messages {
		for _, part := range msg.Parts {
			if part.CacheControl != nil {
				return true
			}
		}
	}
	for _, tool := range req.Tools {
		if tool.CacheControl != nil {
			return true
		}
	}
	return false
}

func ValidateNoScopedCacheControl(req *Request, provider string) error {
	if req == nil {
		return nil
	}
	if !RequestHasExplicitCacheControl(req) {
		return nil
	}
	name := strings.TrimSpace(provider)
	if name == "" {
		name = "provider"
	}
	return fmt.Errorf("%s provider does not support explicit cache control", name)
}

func AddUsageCacheDetails(dst *Usage, details map[string]int) {
	if dst == nil || len(details) == 0 {
		return
	}
	if dst.Cache.Details == nil {
		dst.Cache.Details = make(map[string]int, len(details))
	}
	for key, val := range details {
		if strings.TrimSpace(key) == "" || val == 0 {
			continue
		}
		dst.Cache.Details[key] = val
	}
	if len(dst.Cache.Details) == 0 {
		dst.Cache.Details = nil
	}
}

func MessageText(msg Message) (string, error) {
	parts := NormalizeMessageParts(msg)
	if len(parts) == 0 {
		return "", nil
	}
	var b strings.Builder
	for _, part := range parts {
		if err := ValidatePart(part); err != nil {
			return "", err
		}
		if part.Type != PartTypeText {
			return "", fmt.Errorf("unsupported part type %q", part.Type)
		}
		b.WriteString(part.Text)
	}
	return b.String(), nil
}

func NormalizeTextOnlyMessage(msg Message) (Message, error) {
	text, err := MessageText(msg)
	if err != nil {
		return Message{}, err
	}
	out := msg
	out.Content = text
	out.Parts = nil
	return out, nil
}

func NormalizeTextOnlyMessages(messages []Message) ([]Message, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	out := make([]Message, 0, len(messages))
	for i, msg := range messages {
		normalized, err := NormalizeTextOnlyMessage(msg)
		if err != nil {
			return nil, fmt.Errorf("message[%d]: %w", i, err)
		}
		out = append(out, normalized)
	}
	return out, nil
}

func EnsureResultParts(result *Result) {
	if result == nil {
		return
	}
	if len(result.Parts) > 0 || result.Text == "" {
		return
	}
	result.Parts = []Part{TextPart(result.Text)}
}
