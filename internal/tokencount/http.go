// Package tokencount handles native HTTP token-counting responses.
package tokencount

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/httputil"
)

// Post sends a counting request without inference, follows only same-origin
// redirects, and requires an explicit non-negative integer in the response.
func Post(ctx context.Context, endpoint string, headers http.Header, payload any, field string) (int, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	if (req.URL.Scheme != "https" && req.URL.Scheme != "http") || req.URL.Host == "" || req.URL.User != nil {
		return 0, fmt.Errorf("invalid token-counting endpoint")
	}
	req.Header = headers.Clone()
	req.Header.Set("Content-Type", "application/json")
	client := *httputil.ClientForContext(ctx)
	policy := client.CheckRedirect
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if next.URL.Scheme != req.URL.Scheme || !strings.EqualFold(next.URL.Host, req.URL.Host) || next.URL.User != nil {
			return fmt.Errorf("token-counting redirect must stay on the configured origin")
		}
		if policy != nil {
			return policy(next, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, err = httputil.ReadBody(resp.Body)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented {
		return 0, fmt.Errorf("%w: status %d", chat.ErrTokenCountUnsupported, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("token counting: status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return 0, fmt.Errorf("token counting: invalid response: %w", err)
	}
	var count *int
	if err := json.Unmarshal(fields[field], &count); err != nil || count == nil || *count < 0 {
		return 0, fmt.Errorf("token counting: missing or invalid %s", field)
	}
	return *count, nil
}
