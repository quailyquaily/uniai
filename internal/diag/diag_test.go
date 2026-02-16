package diag

import (
	"fmt"
	"testing"
)

type rawErr struct {
	msg string
	raw string
}

func (e *rawErr) Error() string {
	return e.msg
}

func (e *rawErr) RawJSON() string {
	return e.raw
}

func TestLogErrorUsesRawJSON(t *testing.T) {
	var gotLabel, gotPayload string
	LogError(false, func(label, payload string) {
		gotLabel = label
		gotPayload = payload
	}, "openai.chat.response", &rawErr{
		msg: "api failed",
		raw: `{"error":{"message":"bad request"}}`,
	})

	if gotLabel != "openai.chat.response" {
		t.Fatalf("unexpected label: %q", gotLabel)
	}
	if gotPayload != `{"error":{"message":"bad request"}}` {
		t.Fatalf("unexpected payload: %q", gotPayload)
	}
}

func TestLogErrorUsesWrappedRawJSON(t *testing.T) {
	var gotPayload string
	err := fmt.Errorf("wrapped: %w", &rawErr{
		msg: "api failed",
		raw: `{"error":"wrapped"}`,
	})

	LogError(false, func(_ string, payload string) {
		gotPayload = payload
	}, "azure.chat.response", err)

	if gotPayload != `{"error":"wrapped"}` {
		t.Fatalf("unexpected payload: %q", gotPayload)
	}
}

func TestLogErrorFallsBackToErrorString(t *testing.T) {
	var gotPayload string
	LogError(false, func(_ string, payload string) {
		gotPayload = payload
	}, "cloudflare.chat.response", &rawErr{
		msg: "request failed: 500",
		raw: "   ",
	})

	if gotPayload != "request failed: 500" {
		t.Fatalf("unexpected payload: %q", gotPayload)
	}
}

func TestLogErrorIgnoresNilError(t *testing.T) {
	called := false
	LogError(false, func(_, _ string) {
		called = true
	}, "openai.chat.response", nil)

	if called {
		t.Fatalf("callback should not be called for nil error")
	}
}
