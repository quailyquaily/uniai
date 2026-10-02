package openairesp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
)

func TestCountTokensPreservesRawInputAndContext(t *testing.T) {
	input := []any{map[string]any{"role": "user", "content": "raw input"}}
	opts := structs.JSONMap{"input": input, "instructions": "system", "previous_response_id": "resp_previous", "text": map[string]any{"format": map[string]any{"type": "json_schema", "name": "answer", "schema": map[string]any{"type": "object", "properties": map[string]any{}}}}, "store": false}
	before, _ := json.Marshal(opts)
	p, err := New(Config{APIKey: "test", DefaultModel: "gpt-6-sol", HTTPClient: &http.Client{Transport: countRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(body["input"], input) || body["instructions"] != "system" || body["previous_response_id"] != "resp_previous" || !reflect.DeepEqual(body["text"], opts["text"]) {
			t.Fatalf("lost input fields: %#v", body)
		}
		if _, ok := body["store"]; ok {
			t.Fatal("sent store to counter")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"input_tokens":20}`))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CountTokens(context.Background(), &chat.Request{Options: chat.Options{OpenAI: opts}}); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(opts)
	if string(before) != string(after) {
		t.Fatal("mutated caller options")
	}
}

func TestCountTokensRejectsStoredPrompt(t *testing.T) {
	p, err := New(Config{APIKey: "test", DefaultModel: "gpt-6-sol", HTTPClient: &http.Client{Transport: countRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("cannot count unresolved prompt template")
		return nil, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.CountTokens(context.Background(), &chat.Request{Messages: []chat.Message{chat.User("hi")}, Options: chat.Options{OpenAI: structs.JSONMap{"prompt": map[string]any{"id": "pmpt_example"}}}})
	if !errors.Is(err, chat.ErrTokenCountUnsupported) {
		t.Fatalf("expected unsupported template error, got %v", err)
	}
}

type countRoundTripFunc func(*http.Request) (*http.Response, error)

func (f countRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
