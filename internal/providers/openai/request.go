package openai

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/quailyquaily/uniai/internal/httputil"
)

const (
	openAIAPIV1Base = "https://api.openai.com/v1"
)

func doRequest(ctx context.Context, token, base, method, path string, data []byte) ([]byte, error) {
	base = normalizeBase(base)
	url := fmt.Sprintf("%s%s", base, path)
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httputil.DefaultClient.Do(req)
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
