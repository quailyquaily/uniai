package openai

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/chat"
)

func TestHistoryPromptCacheBreakpoint(t *testing.T) {
	cached := chat.WithPartCacheControl(chat.TextPart("stable history"), chat.CacheControl{})
	call := chat.ToolCall{ID: "call_1", Type: "function", Function: chat.ToolCallFunction{Name: "lookup", Arguments: "{}"}}
	for _, model := range []string{"gpt-5.6", "gpt-6-sol"} {
		for _, tc := range []struct {
			name    string
			history chat.Message
			want    string
		}{
			{"user_single", chat.UserParts(cached), `{"role":"user","content":[{"type":"text","text":"stable history","prompt_cache_breakpoint":{"mode":"explicit"}}]}`},
			{"assistant_single", chat.AssistantParts(cached), `{"role":"assistant","content":[{"type":"text","text":"stable history","prompt_cache_breakpoint":{"mode":"explicit"}}]}`},
			{"user_images", chat.UserParts(chat.TextPart("before"), chat.ImageURLPart("https://example.com/a.png"), cached, chat.TextPart("after")), `{"role":"user","content":[{"type":"text","text":"before"},{"type":"image_url","image_url":{"url":"https://example.com/a.png"}},{"type":"text","text":"stable history","prompt_cache_breakpoint":{"mode":"explicit"}},{"type":"text","text":"after"}]}`},
			{"assistant_blocks", chat.Message{Role: chat.RoleAssistant, Content: "ignored legacy content", Parts: []chat.Part{chat.TextPart("before"), cached, chat.TextPart("after")}}, `{"role":"assistant","content":[{"type":"text","text":"before"},{"type":"text","text":"stable history","prompt_cache_breakpoint":{"mode":"explicit"}},{"type":"text","text":"after"}]}`},
			{"assistant_tool_call", chat.Message{Role: chat.RoleAssistant, Parts: []chat.Part{cached}, ToolCalls: []chat.ToolCall{call}}, `{"role":"assistant","content":[{"type":"text","text":"stable history","prompt_cache_breakpoint":{"mode":"explicit"}}],"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}`},
		} {
			t.Run(model+"/"+tc.name, func(t *testing.T) {
				req := &chat.Request{Model: model, Messages: []chat.Message{
					chat.SystemParts(chat.WithPartCacheControl(chat.TextPart("instructions"), chat.CacheControl{})),
					tc.history,
					chat.User("runtime metadata"), chat.User("current request"),
				}}
				before, err := json.Marshal(req)
				if err != nil {
					t.Fatal(err)
				}
				params, err := buildParams(req, "")
				if err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(params)
				if err != nil {
					t.Fatal(err)
				}
				var got map[string]any
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatal(err)
				}
				var want any
				expected := `[{"role":"system","content":[{"type":"text","text":"instructions","prompt_cache_breakpoint":{"mode":"explicit"}}]},` + tc.want + `,{"role":"user","content":"runtime metadata"},{"role":"user","content":"current request"}]`
				if err := json.Unmarshal([]byte(expected), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got["messages"], want) {
					t.Fatalf("unexpected messages:\ngot: %s\nwant: %s", raw, expected)
				}
				if strings.Count(string(raw), `"prompt_cache_breakpoint"`) != 2 {
					t.Fatalf("expected exactly two breakpoints: %s", raw)
				}
				after, err := json.Marshal(req)
				if err != nil {
					t.Fatal(err)
				}
				if string(before) != string(after) {
					t.Fatalf("caller request was mutated: %s", after)
				}
			})
		}
	}
}

func TestHistoryPromptCacheBreakpointRejectsUnsupported(t *testing.T) {
	for _, role := range []string{chat.RoleUser, chat.RoleAssistant} {
		for _, tc := range []struct {
			name  string
			model string
			part  chat.Part
			want  string
		}{
			{"old_model", "gpt-4.1", chat.WithPartCacheControl(chat.TextPart("history"), chat.CacheControl{}), "does not support explicit cache control"},
			{"inline_ttl", "gpt-5.6", chat.WithPartCacheControl(chat.TextPart("history"), chat.CacheTTL5m()), "prompt_cache_options.ttl"},
			{"image", "gpt-5.6", chat.WithPartCacheControl(chat.ImageURLPart("https://example.com/a.png"), chat.CacheControl{}), "text"},
			{"empty_text", "gpt-5.6", chat.WithPartCacheControl(chat.TextPart(" "), chat.CacheControl{}), "non-empty text"},
		} {
			t.Run(role+"/"+tc.name, func(t *testing.T) {
				req := &chat.Request{Model: tc.model, Messages: []chat.Message{{Role: role, Parts: []chat.Part{tc.part}}}}
				_, err := buildParams(req, "")
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("expected %q error, got %v", tc.want, err)
				}
			})
		}
	}
	for _, role := range []string{chat.RoleTool, "unknown"} {
		req := &chat.Request{Model: "gpt-5.6", Messages: []chat.Message{{Role: role, ToolCallID: "call_1", Parts: []chat.Part{chat.WithPartCacheControl(chat.TextPart("history"), chat.CacheControl{})}}}}
		if _, err := buildParams(req, ""); err == nil {
			t.Fatalf("expected cache control rejection for role %q", role)
		}
	}
}
