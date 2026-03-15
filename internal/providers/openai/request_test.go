package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quailyquaily/uniai/internal/httputil"
)

func TestDoRequestDoesNotTruncateLongerContextDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		time.Sleep(60 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"ok": true}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	originalTimeout := httputil.DefaultClient.Timeout
	httputil.DefaultClient.Timeout = 20 * time.Millisecond
	defer func() {
		httputil.DefaultClient.Timeout = originalTimeout
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	resp, err := doRequest(ctx, "test-token", server.URL, http.MethodPost, "/chat/completions", []byte(`{"x":1}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(resp) != "{\"ok\":true}\n" {
		t.Fatalf("unexpected response body: %q", string(resp))
	}
}
