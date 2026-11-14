package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/chat"
)

func TestBuildRequestMarksToolResultCacheBoundary(t *testing.T) {
	ttl := chat.CacheControl{TTL: "5m"}
	marked := chat.TextPart("exit_code: 0")
	marked.CacheControl = &ttl
	req := &chat.Request{Model: "claude-opus-5-5", Messages: []chat.Message{
		chat.User("run it"),
		{Role: chat.RoleAssistant, ToolCalls: []chat.ToolCall{{ID: "call_1", Type: "function", Function: chat.ToolCallFunction{Name: "bash", Arguments: `{"cmd":"ls"}`}}}},
		{Role: chat.RoleTool, ToolCallID: "call_1", Content: "first"},
		{Role: chat.RoleTool, ToolCallID: "call_1", Parts: []chat.Part{marked}},
	}}
	body, err := buildRequest(req, req.Model)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	// Only the marked tool message's tool_result block carries the boundary, with its text.
	if got := strings.Count(string(raw), `"cache_control"`); got != 1 {
		t.Fatalf("cache_control appears %d times: %s", got, raw)
	}
	last := body.Messages[len(body.Messages)-1]
	if len(last.Content) != 1 || last.Content[0].Type != "tool_result" || last.Content[0].Content != "exit_code: 0" ||
		last.Content[0].CacheControl == nil || last.Content[0].CacheControl.TTL != "5m" {
		t.Fatalf("last message = %+v", last.Content)
	}
	if plain := body.Messages[len(body.Messages)-2]; plain.Content[0].CacheControl != nil {
		t.Fatal("an unmarked tool result was marked")
	}
}
