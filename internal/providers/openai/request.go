package openai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/quailyquaily/uniai/internal/httputil"
)

const (
	openAIAPIV1Base = "https://api.openai.com/v1"
)

func doRequest(ctx context.Context, token, base, method, path string, data []byte) ([]byte, error) {
	return doRequestWithContentType(ctx, token, base, method, path, "application/json", data)
}

func doRequestWithContentType(ctx context.Context, token, base, method, path, contentType string, data []byte) ([]byte, error) {
	return doRequestWithBody(ctx, token, base, method, path, contentType, bytes.NewBuffer(data))
}

// doRequestWithBody sends body as is. A body without a known length (such as
// an io.Pipe) is sent with chunked transfer encoding.
func doRequestWithBody(ctx context.Context, token, base, method, path, contentType string, body io.Reader) ([]byte, error) {
	base = normalizeBase(base)
	url := fmt.Sprintf("%s%s", base, path)
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httputil.ClientForContext(ctx).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respData, err := httputil.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return respData, fmt.Errorf("openai API request failed with status %d: %s", resp.StatusCode, string(respData))
	}

	return respData, nil
}

func normalizeBase(base string) string {
	if base == "" {
		return openAIAPIV1Base
	}
	trimmed := strings.TrimRight(base, "/")
	if strings.HasSuffix(trimmed, "/v1") {
		return trimmed
	}
	return trimmed + "/v1"
}
