package evaluate

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidRequest  = errors.New("invalid evaluate request")
	ErrUnsupported     = errors.New("unsupported evaluate capability")
	ErrInvalidResponse = errors.New("invalid evaluate response")
)

// APIError preserves a native provider's HTTP failure. Body is intentionally
// excluded from Error because it may contain application data.
type APIError struct {
	Provider   string
	StatusCode int
	Body       []byte
	RetryAfter string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s evaluate: HTTP %d", e.Provider, e.StatusCode)
}
