package anthropic

import (
	"context"
	"fmt"
	"net/http"

	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/httputil"
	"github.com/quailyquaily/uniai/internal/tokencount"
)

// CountTokens reuses the Messages input mapping without creating a message.
func (p *Provider) CountTokens(ctx context.Context, req *chat.Request) (*chat.TokenCount, error) {
	if req == nil {
		return nil, fmt.Errorf("anthropic request is nil")
	}
	if p.cfg.CredentialSource != nil {
		return nil, fmt.Errorf("%w: Claude subscription credentials", chat.ErrTokenCountUnsupported)
	}
	if p.cfg.APIKey == "" {
		return nil, fmt.Errorf("anthropic api key is required")
	}
	model := req.Model
	if model == "" {
		model = p.cfg.DefaultModel
	}
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}
	body, err := buildRequest(req, model)
	if err != nil {
		return nil, err
	}
	// The count endpoint accepts input-affecting fields, not generation controls.
	payload := struct {
		Model        string                 `json:"model"`
		Messages     []anthropicMessage     `json:"messages"`
		System       any                    `json:"system,omitempty"`
		Tools        []anthropicTool        `json:"tools,omitempty"`
		ToolChoice   *anthropicToolChoice   `json:"tool_choice,omitempty"`
		Thinking     *anthropicThinking     `json:"thinking,omitempty"`
		OutputConfig *anthropicOutputConfig `json:"output_config,omitempty"`
	}{body.Model, body.Messages, body.System, body.Tools, body.ToolChoice, body.Thinking, body.OutputConfig}
	headers := make(http.Header)
	headers.Set("x-api-key", p.cfg.APIKey)
	headers.Set("anthropic-version", "2023-06-01")
	httputil.ApplyHeaders(headers, p.cfg.Headers)
	n, err := tokencount.Post(ctx, messagesURL(p.cfg.APIBase)+"/count_tokens", headers, payload, "input_tokens")
	if err != nil {
		return nil, fmt.Errorf("anthropic CountTokens: %w", err)
	}
	return &chat.TokenCount{Model: body.Model, InputTokens: n}, nil
}
