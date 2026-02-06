package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/quailyquaily/uniai/internal/httputil"
)

const defaultAPIBase = "https://api.cloudflare.com/client/v4"

type apiMessage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type apiEnvelope struct {
	Success  bool            `json:"success"`
	Errors   []apiMessage    `json:"errors"`
	Messages []apiMessage    `json:"messages"`
	Result   json.RawMessage `json:"result"`
}

func RunJSON(ctx context.Context, token, base, accountID, model string, payload any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return Run(ctx, token, base, accountID, model, "application/json", body)
}

func Run(ctx context.Context, token, base, accountID, model, contentType string, body []byte) (json.RawMessage, error) {
	url, err := buildRunURL(base, accountID, model)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

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
		return nil, fmt.Errorf("cloudflare API request failed with status %d: %s", resp.StatusCode, string(respData))
	}

	var envelope apiEnvelope
	if err := json.Unmarshal(respData, &envelope); err != nil {
		return nil, err
	}
	if !envelope.Success {
		return nil, fmt.Errorf("cloudflare API request failed: %s", formatMessages(envelope.Errors, envelope.Messages))
	}
	if len(envelope.Errors) > 0 {
		return nil, fmt.Errorf("cloudflare API errors: %s", formatMessages(envelope.Errors, nil))
	}
	return envelope.Result, nil
}

func buildRunURL(base, accountID, model string) (string, error) {
	if accountID == "" {
		return "", fmt.Errorf("cloudflare account id is required")
	}
	if model == "" {
		return "", fmt.Errorf("model is required")
	}
	base = normalizeBase(base)
	model = strings.TrimPrefix(model, "/")
	return fmt.Sprintf("%s/accounts/%s/ai/run/%s", base, accountID, model), nil
}

func normalizeBase(base string) string {
	if base == "" {
		return defaultAPIBase
	}
	trimmed := strings.TrimRight(base, "/")
	if strings.HasSuffix(trimmed, "/client/v4") {
		return trimmed
	}
	if strings.Contains(trimmed, "/client/v4/") {
		return strings.TrimRight(trimmed, "/")
	}
	return trimmed + "/client/v4"
}

func formatMessages(primary, secondary []apiMessage) string {
	msgs := make([]apiMessage, 0, len(primary)+len(secondary))
	msgs = append(msgs, primary...)
	msgs = append(msgs, secondary...)
	if len(msgs) == 0 {
		return "unknown error"
	}
	parts := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		if msg.Code != 0 {
			parts = append(parts, fmt.Sprintf("%d: %s", msg.Code, msg.Message))
			continue
		}
		if msg.Message != "" {
			parts = append(parts, msg.Message)
		}
	}
	if len(parts) == 0 {
		return "unknown error"
	}
	return strings.Join(parts, "; ")
}
