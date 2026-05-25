package oaicompat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/quailyquaily/uniai/chat"
)

func TestEnsureChatCompletionStreamIncludesUsage(t *testing.T) {
	params := openai.ChatCompletionNewParams{
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeObfuscation: openai.Bool(false),
			IncludeUsage:       openai.Bool(false),
		},
	}

	ensureChatCompletionStreamIncludesUsage(&params)

	if !params.StreamOptions.IncludeUsage.Valid() || !params.StreamOptions.IncludeUsage.Value {
		t.Fatalf("expected include_usage=true, got %#v", params.StreamOptions.IncludeUsage)
	}
	if !params.StreamOptions.IncludeObfuscation.Valid() || params.StreamOptions.IncludeObfuscation.Value {
		t.Fatalf("expected include_obfuscation to stay false, got %#v", params.StreamOptions.IncludeObfuscation)
	}
}

func TestAccumulatedToResultReadsTopLevelCachedTokensFallback(t *testing.T) {
	var resp openai.ChatCompletion
	if err := json.Unmarshal([]byte(`{
		"model": "kimi-k2.6",
		"choices": [
			{
				"message": {
					"role": "assistant",
					"content": "hello"
				}
			}
		],
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 3,
			"total_tokens": 13,
			"cached_tokens": 7
		}
	}`), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	result := accumulatedToResult(&resp)
	if result.Usage.Cache.CachedInputTokens != 7 {
		t.Fatalf("unexpected cache usage: %#v", result.Usage.Cache)
	}
}

func TestChatStreamExposesRawChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(t, w, `{"id":"chatcmpl_test","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"think "},"finish_reason":null}]}`)
		writeSSE(t, w, `{"id":"chatcmpl_test","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{"reasoning_content":"first"},"finish_reason":null}]}`)
		writeSSE(t, w, `{"id":"chatcmpl_test","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`)
		writeSSE(t, w, `{"id":"chatcmpl_test","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		writeSSE(t, w, `{"id":"chatcmpl_test","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3,"custom_usage_tokens":99}}`)
		if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
			t.Fatalf("write done: %v", err)
		}
	}))
	defer server.Close()

	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(server.URL+"/v1"),
	)
	messages, err := ToMessages([]chat.Message{chat.User("hello")}, "gpt-test")
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel("gpt-test"),
		Messages: messages,
	}

	var deltaRaw openai.ChatCompletionChunk
	var doneEvent chat.StreamEvent
	result, err := ChatStream(context.Background(), &client, params, func(ev chat.StreamEvent) error {
		if ev.Delta == "hello" {
			chunk, ok := ev.Raw.(openai.ChatCompletionChunk)
			if !ok {
				t.Fatalf("delta raw type = %T", ev.Raw)
			}
			deltaRaw = chunk
		}
		if ev.Done {
			doneEvent = ev
		}
		return nil
	})
	if err != nil {
		t.Fatalf("chat stream: %v", err)
	}

	if !strings.Contains(deltaRaw.RawJSON(), `"content":"hello"`) {
		t.Fatalf("expected raw delta chunk, got %q", deltaRaw.RawJSON())
	}
	if len(result.Messages) != 1 || result.Messages[0].ReasoningContent != "think first" {
		t.Fatalf("unexpected replay messages: %#v", result.Messages)
	}
	doneChunks, ok := doneEvent.Raw.([]openai.ChatCompletionChunk)
	if !ok {
		t.Fatalf("done raw type = %T", doneEvent.Raw)
	}
	resultChunks, ok := result.Raw.([]openai.ChatCompletionChunk)
	if !ok {
		t.Fatalf("result raw type = %T", result.Raw)
	}
	if len(doneChunks) != 5 || len(resultChunks) != 5 {
		t.Fatalf("expected 5 raw chunks, got done=%d result=%d", len(doneChunks), len(resultChunks))
	}
	last := doneChunks[len(doneChunks)-1]
	if !strings.Contains(last.RawJSON(), `"custom_usage_tokens":99`) {
		t.Fatalf("expected final raw usage chunk to keep extension fields, got %q", last.RawJSON())
	}
	if !strings.Contains(last.Usage.RawJSON(), `"custom_usage_tokens":99`) {
		t.Fatalf("expected raw usage to keep extension fields, got %q", last.Usage.RawJSON())
	}
	if result.Usage.TotalTokens != 3 || doneEvent.Usage == nil || doneEvent.Usage.TotalTokens != 3 {
		t.Fatalf("unexpected usage: result=%#v done=%#v", result.Usage, doneEvent.Usage)
	}
}

func TestStreamToolCallNameDeltaNormalization(t *testing.T) {
	tests := []struct {
		name     string
		deltas   []string
		wantName string
		wantEmit string
	}{
		{
			name:     "standard fragments",
			deltas:   []string{"read", "_file"},
			wantName: "read_file",
			wantEmit: "read|_file",
		},
		{
			name:     "repeated full name",
			deltas:   []string{"read_file", "read_file", "read_file"},
			wantName: "read_file",
			wantEmit: "read_file||",
		},
		{
			name:     "cumulative name",
			deltas:   []string{"read", "read_file", "read_file"},
			wantName: "read_file",
			wantEmit: "read|_file|",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var call streamToolCallState
			emitted := make([]string, 0, len(tt.deltas))
			for _, delta := range tt.deltas {
				emitted = append(emitted, call.addNameDelta(delta))
			}
			if call.Name != tt.wantName {
				t.Fatalf("name = %q, want %q", call.Name, tt.wantName)
			}
			if got := strings.Join(emitted, "|"); got != tt.wantEmit {
				t.Fatalf("emitted deltas = %q, want %q", got, tt.wantEmit)
			}
		})
	}
}

func TestChatStreamNormalizesRepeatedFullToolCallNameDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(t, w, `{"id":"chatcmpl_tool","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\""}}]},"finish_reason":null}]}`)
		writeSSE(t, w, `{"id":"chatcmpl_tool","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"read_file","arguments":":\"README.md\"}"}}]},"finish_reason":null}]}`)
		writeSSE(t, w, `{"id":"chatcmpl_tool","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
		writeSSE(t, w, `{"id":"chatcmpl_tool","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
		if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
			t.Fatalf("write done: %v", err)
		}
	}))
	defer server.Close()

	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(server.URL+"/v1"),
	)
	messages, err := ToMessages([]chat.Message{chat.User("read README")}, "gpt-test")
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel("gpt-test"),
		Messages: messages,
	}

	var toolDeltas []chat.ToolCallDelta
	result, err := ChatStream(context.Background(), &client, params, func(ev chat.StreamEvent) error {
		if ev.ToolCallDelta != nil {
			toolDeltas = append(toolDeltas, *ev.ToolCallDelta)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("chat stream: %v", err)
	}

	if len(toolDeltas) != 2 {
		t.Fatalf("expected two tool call deltas, got %#v", toolDeltas)
	}
	if toolDeltas[0].Name != "read_file" || toolDeltas[1].Name != "" {
		t.Fatalf("unexpected normalized tool call name deltas: %#v", toolDeltas)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", result.ToolCalls)
	}
	call := result.ToolCalls[0]
	if call.ID != "call_1" || call.Function.Name != "read_file" {
		t.Fatalf("unexpected normalized tool call: %#v", call)
	}
	if call.Function.Arguments != `{"path":"README.md"}` {
		t.Fatalf("unexpected tool call arguments: %q", call.Function.Arguments)
	}
	if len(result.Messages) != 1 || len(result.Messages[0].ToolCalls) != 1 {
		t.Fatalf("expected replay message tool call, got %#v", result.Messages)
	}
	if result.Messages[0].ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("unexpected replay tool call name: %#v", result.Messages[0].ToolCalls[0])
	}
}

func TestChatStreamNormalizesNegativeToolCallIndexes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(t, w, `{"id":"chatcmpl_tool","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":-1,"delta":{"role":"assistant","tool_calls":[{"index":-1,"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\""}}]},"finish_reason":null}]}`)
		writeSSE(t, w, `{"id":"chatcmpl_tool","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":-1,"delta":{"tool_calls":[{"index":-1,"function":{"arguments":":\"README.md\"}"}}]},"finish_reason":null}]}`)
		writeSSE(t, w, `{"id":"chatcmpl_tool","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":-1,"delta":{},"finish_reason":"tool_calls"}]}`)
		writeSSE(t, w, `{"id":"chatcmpl_tool","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
		if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
			t.Fatalf("write done: %v", err)
		}
	}))
	defer server.Close()

	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(server.URL+"/v1"),
	)
	messages, err := ToMessages([]chat.Message{chat.User("read README")}, "gpt-test")
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel("gpt-test"),
		Messages: messages,
	}

	var toolDeltas []chat.ToolCallDelta
	result, err := ChatStream(context.Background(), &client, params, func(ev chat.StreamEvent) error {
		if ev.ToolCallDelta != nil {
			toolDeltas = append(toolDeltas, *ev.ToolCallDelta)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("chat stream: %v", err)
	}

	if len(toolDeltas) != 2 {
		t.Fatalf("expected two tool call deltas, got %#v", toolDeltas)
	}
	if toolDeltas[0].Index != 0 || toolDeltas[1].Index != 0 {
		t.Fatalf("negative tool call indexes should normalize to 0, got %#v", toolDeltas)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", result.ToolCalls)
	}
	call := result.ToolCalls[0]
	if call.ID != "call_1" || call.Function.Name != "read_file" || call.Function.Arguments != `{"path":"README.md"}` {
		t.Fatalf("unexpected tool call: %#v", call)
	}
	if result.Usage.TotalTokens != 3 {
		t.Fatalf("unexpected usage: %#v", result.Usage)
	}
}

func writeSSE(t *testing.T, w http.ResponseWriter, data string) {
	t.Helper()
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		t.Fatalf("write sse: %v", err)
	}
}
