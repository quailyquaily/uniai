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
