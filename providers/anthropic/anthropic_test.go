package anthropic

import (
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/chat"
)

func TestBuildRequestMapsUserImageBase64Part(t *testing.T) {
	req := &chat.Request{
		Model: "claude-sonnet-4-20250514",
		Messages: []chat.Message{
			chat.UserParts(
				chat.TextPart("describe this"),
				chat.ImageBase64Part("image/jpeg", "QUJD"),
			),
		},
	}

	body, err := buildRequest(req, req.Model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(body.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(body.Messages))
	}
	msg := body.Messages[0]
	if msg.Role != "user" {
		t.Fatalf("expected user role, got %q", msg.Role)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("expected 2 content parts, got %d", len(msg.Content))
	}
	if msg.Content[0].Type != "text" || msg.Content[0].Text != "describe this" {
		t.Fatalf("unexpected first content part: %#v", msg.Content[0])
	}
	if msg.Content[1].Type != "image" || msg.Content[1].Source == nil {
		t.Fatalf("expected image content part, got %#v", msg.Content[1])
	}
	if msg.Content[1].Source.Type != "base64" {
		t.Fatalf("expected base64 source, got %#v", msg.Content[1].Source)
	}
	if msg.Content[1].Source.MediaType != "image/jpeg" || msg.Content[1].Source.Data != "QUJD" {
		t.Fatalf("unexpected base64 payload: %#v", msg.Content[1].Source)
	}
}

func TestBuildRequestMapsUserImageURLPart(t *testing.T) {
	req := &chat.Request{
		Model: "claude-sonnet-4-20250514",
		Messages: []chat.Message{
			chat.UserParts(
				chat.TextPart("what is in this image"),
				chat.ImageURLPart("https://example.com/cat.png"),
			),
		},
	}

	body, err := buildRequest(req, req.Model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msg := body.Messages[0]
	if len(msg.Content) != 2 {
		t.Fatalf("expected 2 content parts, got %d", len(msg.Content))
	}
	if msg.Content[1].Type != "image" || msg.Content[1].Source == nil {
		t.Fatalf("expected image part, got %#v", msg.Content[1])
	}
	if msg.Content[1].Source.Type != "url" || msg.Content[1].Source.URL != "https://example.com/cat.png" {
		t.Fatalf("unexpected image url source: %#v", msg.Content[1].Source)
	}
}

func TestBuildRequestDefaultsImageBase64MIMEType(t *testing.T) {
	req := &chat.Request{
		Model: "claude-sonnet-4-20250514",
		Messages: []chat.Message{
			chat.UserParts(chat.ImageBase64Part("", "QUJD")),
		},
	}

	body, err := buildRequest(req, req.Model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msg := body.Messages[0]
	if len(msg.Content) != 1 || msg.Content[0].Source == nil {
		t.Fatalf("unexpected content: %#v", msg.Content)
	}
	if got := msg.Content[0].Source.MediaType; got != "image/png" {
		t.Fatalf("expected default mime image/png, got %q", got)
	}
}

func TestBuildRequestRejectsNonTextPartForSystemRole(t *testing.T) {
	req := &chat.Request{
		Model: "claude-sonnet-4-20250514",
		Messages: []chat.Message{
			chat.SystemParts(chat.ImageURLPart("https://example.com/not-allowed.png")),
			chat.User("hello"),
		},
	}

	_, err := buildRequest(req, req.Model)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), `role "system"`) || !strings.Contains(err.Error(), `unsupported part type "image_url"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildRequestMapsReasoningBudget(t *testing.T) {
	budget := 4096
	req := &chat.Request{
		Model: "claude-opus-4-5-20250929",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			ReasoningBudget: &budget,
		},
	}

	body, err := buildRequest(req, req.Model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body.Thinking == nil || body.Thinking.Type != "enabled" || body.Thinking.BudgetTokens == nil || *body.Thinking.BudgetTokens != budget {
		t.Fatalf("unexpected thinking config: %#v", body.Thinking)
	}
}

func TestBuildRequestMapsReasoningDetailsToAdaptiveThinking(t *testing.T) {
	req := &chat.Request{
		Model: "claude-sonnet-4-6-20260201",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			ReasoningDetails: true,
		},
	}

	body, err := buildRequest(req, req.Model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body.Thinking == nil || body.Thinking.Type != "adaptive" {
		t.Fatalf("expected adaptive thinking, got %#v", body.Thinking)
	}
}

func TestBuildRequestRejectsReasoningDetailsWithoutBudgetOnManualModel(t *testing.T) {
	req := &chat.Request{
		Model: "claude-opus-4-5-20250929",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			ReasoningDetails: true,
		},
	}

	_, err := buildRequest(req, req.Model)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "WithReasoningBudgetTokens") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildRequestRejectsReasoningBudgetOnEffortModel(t *testing.T) {
	budget := 4096
	req := &chat.Request{
		Model: "claude-sonnet-4-6-20260201",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			ReasoningBudget: &budget,
		},
	}

	_, err := buildRequest(req, req.Model)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "reasoning effort") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestToResultParsesReasoningDetails(t *testing.T) {
	out, err := toResult(&anthropicResponse{
		Model: "claude-opus-4-5-20250929",
		Content: []anthropicContentPart{
			{Type: "thinking", Thinking: "I should inspect the file", Signature: "sig1"},
			{Type: "redacted_thinking", Data: "opaque"},
			{Type: "text", Text: "done"},
		},
	}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Text != "done" {
		t.Fatalf("unexpected text: %q", out.Text)
	}
	if out.Reasoning == nil || len(out.Reasoning.Summary) != 1 || out.Reasoning.Summary[0] != "I should inspect the file" {
		t.Fatalf("unexpected reasoning summary: %#v", out.Reasoning)
	}
	if len(out.Reasoning.Blocks) != 2 {
		t.Fatalf("unexpected reasoning blocks: %#v", out.Reasoning)
	}
}
