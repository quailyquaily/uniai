package image

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientCreateStoresUpstreamRawResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"created":         1,
			"upstream_marker": "raw-only",
			"data": []map[string]any{
				{"b64_json": "QUJD"},
			},
		})
	}))
	defer server.Close()

	client := New(Config{
		OpenAIAPIKey:  "test-key",
		OpenAIAPIBase: server.URL,
	})
	result, err := client.Create(context.Background(), Image("gpt-image-2", "draw a cat"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if !strings.Contains(string(result.Raw), `"upstream_marker":"raw-only"`) {
		t.Fatalf("raw does not contain upstream response: %s", string(result.Raw))
	}
}
