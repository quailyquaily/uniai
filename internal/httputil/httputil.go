package httputil

import (
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
