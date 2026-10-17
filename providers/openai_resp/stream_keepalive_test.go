package openairesp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
	"github.com/quailyquaily/uniai/chat"
)

// keepAliveStream interleaves SSE comments and data-less events with a normal
// response stream. Older SDK decoders dispatched these frames and failed with
// "unexpected end of JSON input".
const keepAliveStream = ": keepalive\n\n" +
	"event: ping\n\n" +
	"event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_test\",\"status\":\"in_progress\",\"output\":[]}}\n\n" +
	": ping\n\n" +
	"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"delta\":\"hello\"}\n\n" +
	"event: ping\n\n" +
	"event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_test\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}]}}\n\n"

func TestChatStreamSkipsKeepAliveFrames(t *testing.T) {
	for _, tc := range []struct {
		name      string
		streaming bool
	}{
		{name: "NewStreaming", streaming: true},
		{name: "manual decoder", streaming: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := New(Config{APIKey: "test", DefaultModel: "gpt-6-sol", HTTPClient: &http.Client{Transport: countRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: 200,
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader(keepAliveStream)),
				}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			req := &chat.Request{Messages: []chat.Message{chat.User("hi")}}
			var deltas []string
			if tc.streaming {
				req.Options.OnStream = func(ev chat.StreamEvent) error {
					if ev.Delta != "" {
						deltas = append(deltas, ev.Delta)
					}
					return nil
				}
			}
			result, err := p.Chat(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if result.Text != "hello" || result.FinishReason != "stop" {
				t.Fatalf("result = %+v", result)
			}
			if tc.streaming && strings.Join(deltas, "") != "hello" {
				t.Fatalf("deltas = %q", deltas)
			}
		})
	}
}

// fakeDecoder replays events as given, including data-less ones, independent of
// the SDK decoder's own filtering.
type fakeDecoder struct {
	events []ssestream.Event
	index  int
}

func (d *fakeDecoder) Next() bool {
	d.index++
	return d.index <= len(d.events)
}
func (d *fakeDecoder) Event() ssestream.Event { return d.events[d.index-1] }
func (d *fakeDecoder) Close() error           { return nil }
func (d *fakeDecoder) Err() error             { return nil }

func TestResponseEventDecoderSkipsEmptyEvents(t *testing.T) {
	completed := `{"type":"response.completed","response":{"id":"resp_test","status":"completed","output":[]}}`
	inner := &fakeDecoder{events: []ssestream.Event{
		{},
		{Type: "ping"},
		{Type: "ping", Data: []byte(" \n")},
		{Type: "response.completed", Data: []byte(completed)},
	}}
	stream := ssestream.NewStream[responses.ResponseStreamEventUnion](&responseEventDecoder{Decoder: inner}, nil)
	result, err := consumeResponseStream(stream, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.FinishReason != "stop" {
		t.Fatalf("finish reason = %q", result.FinishReason)
	}
}
