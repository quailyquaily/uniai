package httputil

import (
	"net/http"
	"strings"
)

// CloneHeaders returns a shallow copy of headers with blank keys removed.
func CloneHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}

	out := make(map[string]string, len(headers))
	for key, value := range headers {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ApplyHeaders sets extra headers on an existing header map.
func ApplyHeaders(dst http.Header, headers map[string]string) {
	for key, value := range headers {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		dst.Set(key, value)
	}
}
