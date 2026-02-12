package chat

import "testing"

func TestNormalizeMessagePartsPrefersParts(t *testing.T) {
	msg := Message{
		Role:    RoleUser,
		Content: "legacy",
		Parts: []Part{
			TextPart("new"),
		},
	}

	got := NormalizeMessageParts(msg)
	if len(got) != 1 || got[0].Text != "new" {
		t.Fatalf("expected parts to be preferred, got %#v", got)
	}
}

func TestNormalizeMessagePartsFromContent(t *testing.T) {
	msg := User("hello")
	got := NormalizeMessageParts(msg)
	if len(got) != 1 {
		t.Fatalf("expected one normalized part, got %d", len(got))
	}
	if got[0].Type != PartTypeText || got[0].Text != "hello" {
		t.Fatalf("unexpected normalized part: %#v", got[0])
	}
}

func TestBuildRequestRejectsUnsupportedPartType(t *testing.T) {
	_, err := BuildRequest(
		WithMessages(UserParts(Part{Type: "audio_base64", DataBase64: "abc"})),
	)
	if err == nil {
		t.Fatalf("expected unsupported part error")
	}
}

func TestMessageTextRejectsNonTextPart(t *testing.T) {
	_, err := MessageText(UserParts(ImageURLPart("https://example.com/a.png")))
	if err == nil {
		t.Fatalf("expected non-text error")
	}
}

func TestBuildRequestRejectsNonTextPartForAssistantRole(t *testing.T) {
	_, err := BuildRequest(
		WithMessages(AssistantParts(ImageURLPart("https://example.com/a.png"))),
	)
	if err == nil {
		t.Fatalf("expected role constraint error")
	}
}

func TestNormalizeTextOnlyMessages(t *testing.T) {
	msgs, err := NormalizeTextOnlyMessages([]Message{
		UserParts(TextPart("hello")),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "hello" || len(msgs[0].Parts) != 0 {
		t.Fatalf("unexpected normalized messages: %#v", msgs)
	}
}
