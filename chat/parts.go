package chat

import (
	"fmt"
	"strings"
)

func CloneParts(parts []Part) []Part {
	if len(parts) == 0 {
		return nil
	}
	out := make([]Part, len(parts))
	copy(out, parts)
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
	switch part.Type {
	case PartTypeText:
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
