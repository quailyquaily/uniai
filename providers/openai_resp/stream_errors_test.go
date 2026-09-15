package openairesp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
	"github.com/quailyquaily/uniai/chat"
)

type trackedStreamBody struct {
	io.Reader
	closed bool
}

func (b *trackedStreamBody) Close() error { b.closed = true; return nil }

type afterStreamReader struct{}

func (afterStreamReader) Read([]byte) (int, error) { return 0, errors.New("read past terminal event") }

func TestToResultFinishReasonPrecedence(t *testing.T) {
	text := `{"type":"message","content":[{"type":"output_text","text":""},{"type":"output_text","text":"hello"},{"type":"output_text","text":" world"}]}`
	tool := `{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"}`
	refusal := `{"type":"message","content":[{"type":"refusal","refusal":""}]}`
	for _, tc := range []struct{ name, status, extraOutput, finish string }{
		{name: "text", status: "completed", finish: "stop"},
		{name: "tool", status: "completed", extraOutput: "," + tool, finish: "tool_calls"},
		{name: "empty refusal", status: "completed", extraOutput: "," + refusal, finish: "content_filter"},
		{name: "refusal before tool", status: "completed", extraOutput: "," + refusal + "," + tool, finish: "content_filter"},
		{name: "refusal after tool", status: "completed", extraOutput: "," + tool + "," + refusal, finish: "content_filter"},
		{name: "missing status", extraOutput: "," + tool + "," + refusal},
		{name: "in progress", status: "in_progress", extraOutput: "," + tool + "," + refusal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var response responses.Response
			if err := json.Unmarshal([]byte(`{"status":"`+tc.status+`","output":[`+text+tc.extraOutput+`]}`), &response); err != nil {
				t.Fatal(err)
			}
			result := toResult(&response)
			if result.FinishReason != tc.finish {
				t.Fatalf("finish reason = %q, want %q", result.FinishReason, tc.finish)
			}
			if result.Text != "hello world" || len(result.Parts) != 2 || result.Parts[0].Text != "hello" || result.Parts[1].Text != " world" {
				t.Fatalf("unexpected text or parts: %+v", result)
			}
			if len(result.Messages) == 0 || result.Messages[0].Content != "hello world" || len(result.Messages[0].Parts) != 2 {
				t.Fatalf("unexpected messages: %+v", result.Messages)
			}
		})
	}
}

func TestResponseStreamCompletionStates(t *testing.T) {
	completed := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"status\":\"completed\",\"output\":[]}}\n\n"
	for _, tc := range []struct {
		name, stream, finish, wantErr string
		terminal                      bool
	}{
		{name: "completed", stream: completed, finish: "stop", terminal: true},
		{name: "tool call", stream: "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"lookup\",\"arguments\":\"{}\"}]}}\n\n", finish: "tool_calls", terminal: true},
		{name: "refusal", stream: "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"refusal\",\"refusal\":\"declined\"}]}]}}\n\n", finish: "content_filter", terminal: true},
		{name: "error event", stream: "data: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"Overloaded\"}\n\n" + completed, wantErr: "Overloaded"},
		{name: "failed", stream: "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"message\":\"failed generation\"}}}\n\n", wantErr: "failed generation", terminal: true},
		{name: "incomplete", stream: "data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n", wantErr: "max_output_tokens", terminal: true},
		{name: "early EOF", stream: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n", wantErr: "without a completed response"},
		{name: "empty", wantErr: "without a completed response"},
		{name: "invalid JSON", stream: "data: {bad}\n\n", wantErr: "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reader io.Reader = strings.NewReader(tc.stream)
			if tc.terminal {
				reader = io.MultiReader(reader, afterStreamReader{})
			}
			body := &trackedStreamBody{Reader: reader}
			stream := ssestream.NewStream[responses.ResponseStreamEventUnion](ssestream.NewDecoder(&http.Response{Body: body}), nil)
			var done []chat.StreamEvent
			result, err := consumeResponseStream(stream, false, func(ev chat.StreamEvent) error {
				if ev.Done {
					done = append(done, ev)
				}
				return nil
			})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || result != nil || len(done) != 0 {
					t.Fatalf("result=%+v done=%+v err=%v, want %q", result, done, err, tc.wantErr)
				}
			} else if err != nil || result == nil || result.FinishReason != tc.finish || len(done) != 1 || done[0].FinishReason != tc.finish {
				t.Fatalf("result=%+v done=%+v err=%v, want %q", result, done, err, tc.finish)
			}
			if !body.closed {
				t.Error("response body was not closed")
			}
		})
	}
}

func TestResponseStreamClosesOnCallbackError(t *testing.T) {
	body := &trackedStreamBody{Reader: strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\n")}
	stream := ssestream.NewStream[responses.ResponseStreamEventUnion](ssestream.NewDecoder(&http.Response{Body: body}), nil)
	want := errors.New("consumer stopped")
	_, err := consumeResponseStream(stream, false, func(chat.StreamEvent) error { return want })
	if !errors.Is(err, want) || !body.closed {
		t.Fatalf("closed=%t err=%v", body.closed, err)
	}
}
