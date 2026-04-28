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
	doneChunks, ok := doneEvent.Raw.([]openai.ChatCompletionChunk)
	if !ok {
		t.Fatalf("done raw type = %T", doneEvent.Raw)
	}
	resultChunks, ok := result.Raw.([]openai.ChatCompletionChunk)
	if !ok {
		t.Fatalf("result raw type = %T", result.Raw)
	}
	if len(doneChunks) != 3 || len(resultChunks) != 3 {
		t.Fatalf("expected 3 raw chunks, got done=%d result=%d", len(doneChunks), len(resultChunks))
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

func writeSSE(t *testing.T, w http.ResponseWriter, data string) {
	t.Helper()
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		t.Fatalf("write sse: %v", err)
	}
}
