package jina

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestClassifyInputFormats(t *testing.T) {
	for _, tc := range []struct {
		model string
		input ClassifyInput
		want  any
	}{
		{"jina-embeddings-v3", ClassifyInput{Text: "hello"}, "hello"},
		{"jina-embeddings-v5-text-small", ClassifyInput{Text: "hello"}, "hello"},
		{"jina-embeddings-v5-text-nano", ClassifyInput{Text: "hello"}, "hello"},
		{"jina-embeddings-v4", ClassifyInput{Text: "hello"}, map[string]any{"text": "hello"}},
		{"jina-clip-v2", ClassifyInput{Image: "image-data"}, map[string]any{"image": "image-data"}},
	} {
		t.Run(tc.model, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/classify" || r.Header.Get("Authorization") != "Bearer key" {
					t.Error("wrong endpoint or authorization")
				}
				var body struct {
					Model  string   `json:"model"`
					Input  []any    `json:"input"`
					Labels []string `json:"labels"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Model != tc.model || !reflect.DeepEqual(body.Input, []any{tc.want}) || !reflect.DeepEqual(body.Labels, []string{"greeting", "other"}) {
					t.Errorf("unexpected request: %#v", body)
				}
				w.Write([]byte(`{"data":[]}`))
			}))
			defer server.Close()
			if _, err := Classify(context.Background(), "key", server.URL, tc.model, []string{"greeting", "other"}, []ClassifyInput{tc.input}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTextClassifiersRejectImages(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	for _, model := range []string{"jina-embeddings-v3", "jina-embeddings-v5-text-small", "jina-embeddings-v5-text-nano"} {
		if _, err := Classify(context.Background(), "key", server.URL, model, []string{"one", "two"}, []ClassifyInput{{Image: "image-data"}}); err == nil {
			t.Errorf("%s silently accepted an image", model)
		}
	}
	if calls != 0 {
		t.Fatalf("sent %d invalid requests", calls)
	}
}
