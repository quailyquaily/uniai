package chat

import "testing"

func TestBuildRequestRequiresMessages(t *testing.T) {
	_, err := BuildRequest(WithModel("gpt-4.1-mini"))
	if err == nil {
		t.Fatalf("expected error when messages are missing")
	}
}

func TestWithMessagesAppend(t *testing.T) {
	req, err := BuildRequest(
		WithMessages(User("first")),
		WithMessages(User("second")),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Content != "first" || req.Messages[1].Content != "second" {
		t.Fatalf("unexpected order: %+v", req.Messages)
	}
}

func TestWithReplaceMessages(t *testing.T) {
	req, err := BuildRequest(
		WithMessages(User("first")),
		WithReplaceMessages(User("only")),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.Messages) != 1 || req.Messages[0].Content != "only" {
		t.Fatalf("unexpected messages: %+v", req.Messages)
	}
}

func TestOptions(t *testing.T) {
	req, err := BuildRequest(
		WithMessages(User("hi")),
		WithTemperature(0.7),
		WithTopP(0.9),
		WithMaxTokens(123),
		WithStop("END"),
		WithPresencePenalty(0.1),
		WithFrequencyPenalty(0.2),
		WithUser("u1"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Options.Temperature == nil || *req.Options.Temperature != 0.7 {
		t.Fatalf("temperature not set")
	}
	if req.Options.TopP == nil || *req.Options.TopP != 0.9 {
		t.Fatalf("top_p not set")
	}
	if req.Options.MaxTokens == nil || *req.Options.MaxTokens != 123 {
		t.Fatalf("max_tokens not set")
	}
	if len(req.Options.Stop) != 1 || req.Options.Stop[0] != "END" {
		t.Fatalf("stop not set")
	}
	if req.Options.PresencePenalty == nil || *req.Options.PresencePenalty != 0.1 {
		t.Fatalf("presence penalty not set")
	}
	if req.Options.FrequencyPenalty == nil || *req.Options.FrequencyPenalty != 0.2 {
		t.Fatalf("frequency penalty not set")
	}
	if req.Options.User == nil || *req.Options.User != "u1" {
		t.Fatalf("user not set")
	}
}
