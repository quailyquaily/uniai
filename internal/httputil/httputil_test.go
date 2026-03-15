package httputil

import (
	"context"
	"testing"
	"time"
)

func TestClientForContextUsesDefaultClientWithoutDeadline(t *testing.T) {
	if got := ClientForContext(nil); got != DefaultClient {
		t.Fatalf("expected default client for nil context")
	}

	if got := ClientForContext(context.Background()); got != DefaultClient {
		t.Fatalf("expected default client without deadline")
	}
}

func TestClientForContextDisablesClientTimeoutWhenContextHasDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	got := ClientForContext(ctx)
	if got == DefaultClient {
		t.Fatalf("expected cloned client when context has deadline")
	}
	if got.Timeout != 0 {
		t.Fatalf("expected cloned client timeout to be disabled, got %s", got.Timeout)
	}
}
