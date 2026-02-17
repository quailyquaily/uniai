package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/quailyquaily/uniai"
)

func TestDumpFileName(t *testing.T) {
	ts := time.Date(2026, 2, 17, 8, 9, 10, 0, time.UTC)
	got := dumpFileName(ts)
	want := "2026-02-17_08-09-10.md"
	if got != want {
		t.Fatalf("unexpected dump filename: got %q want %q", got, want)
	}
}

func TestFormatDump(t *testing.T) {
	entries := []traceEntry{
		{
			Label:   "openai.chat.request",
			Payload: `{"model":"gpt-5.2"}`,
			At:      time.Date(2026, 2, 17, 8, 9, 10, 0, time.UTC),
		},
		{
			Label:   "openai.chat.response",
			Payload: "{\"id\":\"chatcmpl_x\"}\nline2",
			At:      time.Date(2026, 2, 17, 8, 9, 11, 0, time.UTC),
		},
	}

	got := formatDump(entries)

	mustContain := []string{
		`## "openai.chat.request"`,
		`* time: 2026-02-17 08:09:10`,
		"* payload: |",
		`{`,
		`  "model": "gpt-5.2"`,
		`}`,
		`## "openai.chat.response"`,
		`* time: 2026-02-17 08:09:11`,
		`{"id":"chatcmpl_x"}`,
		"line2",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Fatalf("formatted dump missing %q, got:\n%s", s, got)
		}
	}
}

func TestNormalizeScene(t *testing.T) {
	got, err := normalizeScene("toolcalling")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "toolcalling" {
		t.Fatalf("unexpected scene: %q", got)
	}

	got, err = normalizeScene("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "none" {
		t.Fatalf("unexpected default scene: %q", got)
	}

	if _, err := normalizeScene("invalid"); err == nil {
		t.Fatalf("expected error for invalid scene")
	}
}

func TestPrettyJSONPayload(t *testing.T) {
	pretty := prettyJSONPayload(`{"a":1,"b":{"c":2}}`)
	if !strings.Contains(pretty, "\"a\": 1") {
		t.Fatalf("expected pretty JSON output, got: %q", pretty)
	}
	if !strings.Contains(pretty, "\"c\": 2") {
		t.Fatalf("expected nested pretty JSON output, got: %q", pretty)
	}

	raw := "{\"a\":1}\nline2"
	if got := prettyJSONPayload(raw); got != raw {
		t.Fatalf("invalid JSON payload should stay unchanged, got: %q", got)
	}
}

func TestAppendToolRoundMessagesPreservesThoughtSignature(t *testing.T) {
	base := []uniai.Message{
		uniai.User("plan route then notify me"),
	}
	resp := &uniai.ChatResult{
		ToolCalls: []uniai.ToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Function: uniai.ToolCallFunction{
					Name:      "get_direction",
					Arguments: `{"from":"tokyo station","to":"shinjuku station"}`,
				},
				ThoughtSignature: "sig_123",
			},
		},
	}

	got := appendToolRoundMessages(base, resp)
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
	if got[1].Role != uniai.RoleAssistant {
		t.Fatalf("expected assistant role, got %q", got[1].Role)
	}
	if len(got[1].ToolCalls) != 1 {
		t.Fatalf("expected assistant tool_calls")
	}
	if got[1].ToolCalls[0].ThoughtSignature != "sig_123" {
		t.Fatalf("expected thought_signature to be preserved")
	}
	if got[2].Role != uniai.RoleTool || got[2].ToolCallID != "call_1" {
		t.Fatalf("unexpected tool result message: %#v", got[2])
	}
}

func TestAppendToolRoundMessagesAssignsToolCallIDWhenMissing(t *testing.T) {
	base := []uniai.Message{
		uniai.User("what is weather"),
	}
	resp := &uniai.ChatResult{
		ToolCalls: []uniai.ToolCall{
			{
				Type: "function",
				Function: uniai.ToolCallFunction{
					Name:      "get_weather",
					Arguments: `{"location":"tokyo"}`,
				},
			},
		},
	}

	got := appendToolRoundMessages(base, resp)
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
	callID := strings.TrimSpace(got[1].ToolCalls[0].ID)
	if callID == "" {
		t.Fatalf("expected generated tool_call id")
	}
	if got[2].ToolCallID != callID {
		t.Fatalf("tool result should reference generated id, got=%q want=%q", got[2].ToolCallID, callID)
	}
}

func TestMockToolResultReturnsJSON(t *testing.T) {
	payload := mockToolResult(uniai.ToolCall{
		Type: "function",
		Function: uniai.ToolCallFunction{
			Name:      "send_message",
			Arguments: `{"to":"alice","message":"hello"}`,
		},
	})

	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("tool result should be json: %v", err)
	}
	if decoded["tool"] != "send_message" {
		t.Fatalf("unexpected tool field: %#v", decoded["tool"])
	}
	if decoded["status"] != "sent" {
		t.Fatalf("unexpected status: %#v", decoded["status"])
	}
}
