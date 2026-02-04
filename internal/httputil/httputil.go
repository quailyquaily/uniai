package httputil

import (
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

// ReadBody reads a response body with a size limit to prevent memory exhaustion.
func ReadBody(body io.ReadCloser) ([]byte, error) {
	return io.ReadAll(io.LimitReader(body, MaxResponseBodySize))
}
