package oaicompat

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/chat"
)

type trackedStreamBody struct {
	io.Reader
	closed bool
}

func (b *trackedStreamBody) Close() error { b.closed = true; return nil }

type afterStreamReader struct{}

func (afterStreamReader) Read([]byte) (int, error) { return 0, errors.New("read past terminal event") }

func TestChatStreamRejectsMissingResponseBody(t *testing.T) {
	for _, resp := range []*http.Response{nil, {}} {
		_, err := ChatStreamFromResponse(resp, false, func(chat.StreamEvent) error {
			t.Fatal("empty response must not emit stream events")
			return nil
		})
		if err == nil || err.Error() != "openai chat stream response is empty" {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestChatStreamCompletionStates(t *testing.T) {
	partial := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n"
	for _, tc := range []struct {
		name, stream, finish, wantErr string
		terminal                      bool
	}{
		{name: "stop", stream: partial + "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n", finish: "stop"},
		{name: "length", stream: "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"length\"}]}\n\n", finish: "length"},
		{name: "tool calls", stream: "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n", finish: "tool_calls"},
		{name: "filter", stream: "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"content_filter\"}]}\n\n", finish: "content_filter"},
		{name: "DONE without reason", stream: partial + "data: [DONE]\n\n", terminal: true},
		{name: "early EOF", stream: partial, wantErr: "finish"},
		{name: "empty", wantErr: "finish"},
		{name: "event error", stream: "data: {\"error\":{\"message\":\"Overloaded\"}}\n\n", wantErr: "Overloaded"},
		{name: "invalid JSON", stream: "data: {bad}\n\n", wantErr: "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reader io.Reader = strings.NewReader(tc.stream)
			if tc.terminal {
				reader = io.MultiReader(reader, afterStreamReader{})
			}
			body := &trackedStreamBody{Reader: reader}
			var done []chat.StreamEvent
			result, err := ChatStreamFromResponse(&http.Response{Body: body}, false, func(ev chat.StreamEvent) error {
				if ev.Done {
					done = append(done, ev)
				}
				return nil
			})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || result != nil || len(done) != 0 {
					t.Fatalf("result=%+v done=%+v err=%v", result, done, err)
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

func TestChatStreamClosesOnCallbackError(t *testing.T) {
	body := &trackedStreamBody{Reader: strings.NewReader("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\"}}]}\n\n")}
	want := errors.New("consumer stopped")
	_, err := ChatStreamFromResponse(&http.Response{Body: body}, false, func(chat.StreamEvent) error { return want })
	if !errors.Is(err, want) || !body.closed {
		t.Fatalf("closed=%t err=%v", body.closed, err)
	}
}
