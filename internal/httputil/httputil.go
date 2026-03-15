package httputil

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	DefaultTimeout      = 120 * time.Second
	MaxResponseBodySize = 100 * 1024 * 1024 // 100 MB
)

// DefaultClient is a shared http.Client with a reasonable timeout.
var DefaultClient = &http.Client{
	Timeout: DefaultTimeout,
}

// ClientForContext returns a client that won't impose a shorter timeout than
// the caller's context deadline. When the caller already set a deadline, rely
// on context cancellation instead of http.Client.Timeout.
func ClientForContext(ctx context.Context) *http.Client {
	if ctx == nil {
		return DefaultClient
	}
	if _, ok := ctx.Deadline(); !ok {
		return DefaultClient
	}
	client := *DefaultClient
	client.Timeout = 0
	return &client
}

// ReadBody reads a response body with a size limit to prevent memory exhaustion.
// Returns an error if the body exceeds MaxResponseBodySize.
func ReadBody(body io.ReadCloser) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, MaxResponseBodySize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxResponseBodySize {
		return nil, fmt.Errorf("response body exceeds %d bytes limit", MaxResponseBodySize)
	}
	return data, nil
}
