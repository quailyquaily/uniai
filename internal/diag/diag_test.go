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

type dumpErr struct {
	msg  string
	dump string
}

func (e *dumpErr) Error() string {
	return e.msg
}

func (e *dumpErr) DumpResponse(_ bool) []byte {
	return []byte(e.dump)
}

type rawAndDumpErr struct {
	msg  string
	raw  string
	dump string
}

func (e *rawAndDumpErr) Error() string {
	return e.msg
}

func (e *rawAndDumpErr) RawJSON() string {
	return e.raw
}

func (e *rawAndDumpErr) DumpResponse(_ bool) []byte {
	return []byte(e.dump)
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

func TestLogErrorEmitsRawTextLabelWhenDumpResponseAvailable(t *testing.T) {
	type event struct {
		label   string
		payload string
	}
	var events []event

	LogError(false, func(label, payload string) {
		events = append(events, event{label: label, payload: payload})
	}, "openai.chat.response", &rawAndDumpErr{
		msg:  "request failed",
		raw:  `{"error":{"message":"bad request"}}`,
		dump: "HTTP/1.1 400 Bad Request\r\nContent-Type: application/json\r\n\r\n{\"error\":{\"message\":\"bad request\"}}",
	})

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].label != "openai.chat.response" {
		t.Fatalf("unexpected first label: %q", events[0].label)
	}
	if events[0].payload != `{"error":{"message":"bad request"}}` {
		t.Fatalf("unexpected first payload: %q", events[0].payload)
	}
	if events[1].label != "openai.chat.response.raw_text" {
		t.Fatalf("unexpected second label: %q", events[1].label)
	}
	if events[1].payload == "" {
		t.Fatalf("expected non-empty raw text payload")
	}
}

func TestLogErrorDoesNotDuplicateWhenPayloadEqualsRawText(t *testing.T) {
	type event struct {
		label   string
		payload string
	}
	var events []event

	LogError(false, func(label, payload string) {
		events = append(events, event{label: label, payload: payload})
	}, "cloudflare.chat.response", &dumpErr{
		msg:  "HTTP/1.1 500 Internal Server Error",
		dump: "HTTP/1.1 500 Internal Server Error",
	})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].label != "cloudflare.chat.response" {
		t.Fatalf("unexpected label: %q", events[0].label)
	}
}

func TestLogErrorWithRawTextUsesProvidedRawText(t *testing.T) {
	type event struct {
		label   string
		payload string
	}
	var events []event

	LogErrorWithRawText(false, func(label, payload string) {
		events = append(events, event{label: label, payload: payload})
	}, "openai.chat.response", &rawErr{
		msg: "request failed",
		raw: `{"error":{"message":"bad request"}}`,
	}, "HTTP/1.1 403 Forbidden\nContent-Type: text/html\n\n<html>forbidden</html>")

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].label != "openai.chat.response" {
		t.Fatalf("unexpected first label: %q", events[0].label)
	}
	if events[1].label != "openai.chat.response.raw_text" {
		t.Fatalf("unexpected second label: %q", events[1].label)
	}
	if events[1].payload == "" {
		t.Fatalf("expected non-empty raw text payload")
	}
}

func TestLogErrorWithRawTextSkipsDuplicateProvidedRawText(t *testing.T) {
	type event struct {
		label   string
		payload string
	}
	var events []event

	LogErrorWithRawText(false, func(label, payload string) {
		events = append(events, event{label: label, payload: payload})
	}, "openai.chat.response", &rawErr{
		msg: "request failed",
		raw: `{"error":"same"}`,
	}, `{"error":"same"}`)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].label != "openai.chat.response" {
		t.Fatalf("unexpected label: %q", events[0].label)
	}
}
