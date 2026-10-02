package gemini

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/httputil"
	"github.com/quailyquaily/uniai/internal/tokencount"
)

// CountTokens includes the complete generateContent input, including tools and
// system instructions, without starting generation.
func (p *Provider) CountTokens(ctx context.Context, req *chat.Request) (*chat.TokenCount, error) {
	if req == nil {
		return nil, fmt.Errorf("gemini request is nil")
	}
	if err := chat.ValidateNoScopedCacheControl(req, "gemini"); err != nil {
		return nil, err
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = strings.TrimSpace(p.cfg.DefaultModel)
	}
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}
	body, err := buildRequest(req, model)
	if err != nil {
		return nil, err
	}
	payload := struct {
		GenerateContentRequest struct {
			*geminiRequest
			Model string `json:"model"`
		} `json:"generateContentRequest"`
	}{}
	payload.GenerateContentRequest.geminiRequest = body
	payload.GenerateContentRequest.Model = "models/" + normalizeGeminiModel(model)
	endpoint := fmt.Sprintf("%s/v1beta/models/%s:countTokens", normalizeGeminiBase(p.cfg.BaseURL), url.PathEscape(normalizeGeminiModel(model)))
	headers := make(http.Header)
	headers.Set("x-goog-api-key", p.cfg.APIKey)
	httputil.ApplyHeaders(headers, p.cfg.Headers)
	n, err := tokencount.Post(ctx, endpoint, headers, payload, "totalTokens")
	if err != nil {
		return nil, fmt.Errorf("gemini CountTokens: %w", err)
	}
	return &chat.TokenCount{Model: model, InputTokens: n}, nil
}
