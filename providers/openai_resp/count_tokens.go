package openairesp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/quailyquaily/uniai/chat"
)

// CountTokens uses the Responses input mapping and the input_tokens endpoint.
func (p *Provider) CountTokens(ctx context.Context, req *chat.Request) (*chat.TokenCount, error) {
	if req == nil {
		return nil, fmt.Errorf("openai responses request is nil")
	}
	if req.Options.OpenAI.HasKey("prompt") {
		return nil, fmt.Errorf("%w: the Responses counting endpoint cannot resolve stored prompt templates", chat.ErrTokenCountUnsupported)
	}
	// An explicit empty input permits tools-only counting without adding a message
	// or weakening the inference builder's required-message validation.
	partial := *req
	if len(req.Messages) == 0 && !req.Options.OpenAI.HasKey("input") {
		partial.Options.OpenAI = maps.Clone(req.Options.OpenAI)
		if partial.Options.OpenAI == nil {
			partial.Options.OpenAI = map[string]any{}
		}
		partial.Options.OpenAI["input"] = []any{}
	}
	params, err := buildParams(&partial, p.defaultModel, p.openAICodex)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	var generated map[string]json.RawMessage
	if err := json.Unmarshal(data, &generated); err != nil {
		return nil, err
	}
	// Preserve raw input items and schema extensions while projecting only the
	// fields accepted by the counting endpoint.
	payload := make(map[string]json.RawMessage)
	for _, key := range []string{"model", "input", "instructions", "tools", "tool_choice", "parallel_tool_calls", "reasoning", "text", "conversation", "previous_response_id", "truncation", "personality"} {
		if value, ok := generated[key]; ok {
			payload[key] = value
		}
	}
	var out struct {
		InputTokens *int `json:"input_tokens"`
	}
	err = p.client.Execute(ctx, http.MethodPost, "responses/input_tokens", payload, &out, option.WithMaxRetries(0))
	if err != nil {
		var apiErr *openai.Error
		if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusMethodNotAllowed || apiErr.StatusCode == http.StatusNotImplemented) {
			return nil, fmt.Errorf("%w: %w", chat.ErrTokenCountUnsupported, err)
		}
		return nil, err
	}
	if out.InputTokens == nil || *out.InputTokens < 0 {
		return nil, fmt.Errorf("openai CountTokens: missing or invalid input_tokens")
	}
	return &chat.TokenCount{Model: string(params.Model), InputTokens: *out.InputTokens}, nil
}
