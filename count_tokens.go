package uniai

import (
	"context"
	"fmt"

	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/providers/anthropic"
	"github.com/quailyquaily/uniai/providers/bedrock"
	"github.com/quailyquaily/uniai/providers/gemini"
	openairesp "github.com/quailyquaily/uniai/providers/openai_resp"
)

type TokenCount = chat.TokenCount

var ErrTokenCountUnsupported = chat.ErrTokenCountUnsupported

// SupportsCountTokens reports local adapter support. An empty provider uses the
// configured default. This does not probe upstream model, region, proxy, or input
// support; CountTokens can still return ErrTokenCountUnsupported or an API error.
func (c *Client) SupportsCountTokens(provider string) bool {
	if provider == "" {
		provider = c.cfg.Provider
	}
	switch provider {
	case "anthropic", "gemini", "openai_resp", "bedrock":
		return true
	case "openai_codex":
		return c.cfg.CodexSubscription == nil
	default:
		return false
	}
}

// CountTokens counts one request's input without running inference, invoking
// callbacks, or adding Usage/Cost. It accepts Chat options and partial inputs,
// including tools-only requests, subject to the counting endpoint's validation.
// Chat Completions, subscription authentication and tool emulation are unsupported.
func (c *Client) CountTokens(ctx context.Context, opts ...chat.Option) (*TokenCount, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	req, err := chat.BuildTokenCountRequest(opts...)
	if err != nil {
		return nil, err
	}
	provider := req.Provider
	if provider == "" {
		provider = c.cfg.Provider
	}
	if provider == "" {
		provider = "openai"
	}
	if !c.SupportsCountTokens(provider) {
		return nil, fmt.Errorf("%w: provider %q", ErrTokenCountUnsupported, provider)
	}
	if len(req.Tools) > 0 && req.Options.ToolsEmulationMode != "" && req.Options.ToolsEmulationMode != chat.ToolsEmulationOff {
		return nil, fmt.Errorf("%w: tool emulation can issue multiple requests", ErrTokenCountUnsupported)
	}
	req.Provider = provider
	req.Options.OnStream = nil
	switch provider {
	case "anthropic":
		p := anthropic.New(anthropic.Config{APIKey: c.cfg.AnthropicAPIKey, APIBase: c.cfg.AnthropicAPIBase, DefaultModel: c.cfg.AnthropicModel, Headers: c.cfg.ChatHeaders})
		return p.CountTokens(ctx, req)
	case "gemini":
		key := c.cfg.GeminiAPIKey
		if key == "" {
			key = c.cfg.OpenAIAPIKey
		}
		p, err := gemini.New(gemini.Config{APIKey: key, BaseURL: c.cfg.GeminiAPIBase, DefaultModel: c.resolveChatRequestedModel(provider, req), Headers: c.cfg.ChatHeaders})
		if err != nil {
			return nil, err
		}
		return p.CountTokens(ctx, req)
	case "openai_resp", "openai_codex":
		p, err := openairesp.New(openairesp.Config{APIKey: c.cfg.OpenAIAPIKey, BaseURL: c.cfg.OpenAIAPIBase, DefaultModel: c.cfg.OpenAIModel, Headers: c.cfg.ChatHeaders, OpenAICodex: provider == "openai_codex"})
		if err != nil {
			return nil, err
		}
		return p.CountTokens(ctx, req)
	case "bedrock":
		p := bedrock.New(bedrock.Config{AwsKey: c.cfg.AwsKey, AwsSecret: c.cfg.AwsSecret, AwsSessionToken: c.cfg.AwsSessionToken, AwsRegion: c.cfg.AwsRegion, ModelArn: c.cfg.AwsBedrockModelArn, Headers: c.cfg.ChatHeaders})
		return p.CountTokens(ctx, req)
	default:
		return nil, fmt.Errorf("%w: provider %q", ErrTokenCountUnsupported, provider)
	}
}
