package uniai

import (
	"context"
	"fmt"

	"github.com/quailyquaily/uniai/evaluate"
	"github.com/quailyquaily/uniai/providers/typesafe"
)

// Evaluate returns one typed judgment for every question over the shared state.
// Emulation, when enabled, is selected before sending any request.
func (c *Client) Evaluate(ctx context.Context, req evaluate.Request) (*evaluate.Result, error) {
	if req.Provider == "" {
		req.Provider = c.cfg.EvaluateProvider
	}
	if req.Provider == "" {
		req.Provider = "typesafe"
	}
	if req.Model == "" {
		req.Model = c.cfg.EvaluateModel
	}
	if req.EmulationMode == "" {
		req.EmulationMode = c.cfg.EvaluateEmulationMode
	}
	if req.EmulationMode == "" {
		req.EmulationMode = evaluate.EmulationOff
	}
	if err := evaluate.ValidateRequest(&req); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.Provider == "typesafe" && req.EmulationMode != evaluate.EmulationForce {
		req.EmulationMode = evaluate.EmulationOff
		p, err := typesafe.New(typesafe.Config{APIKey: c.cfg.TypeSafeAPIKey, BaseURL: c.cfg.TypeSafeAPIBase, HTTPClient: c.cfg.EvaluateHTTPClient, Debug: c.cfg.Debug})
		if err != nil {
			return nil, err
		}
		out, err := p.Evaluate(ctx, &req)
		if err != nil {
			return nil, err
		}
		if out.Usage != nil {
			if cost, ok := c.cfg.Pricing.EstimateEvaluateCost(out.Provider, out.Model, *out.Usage); ok {
				out.Usage.Cost = cost
			}
		}
		return out, nil
	}
	if req.EmulationMode == evaluate.EmulationOff {
		return nil, fmt.Errorf("%w: provider %q has no native Evaluate path", evaluate.ErrUnsupported, req.Provider)
	}
	switch req.Provider {
	case "openai", "deepseek", "xai", "groq", "meta", "openai_resp", "openai_codex", "xai_oauth", "sakana", "gemini", "azure", "anthropic", "claude_oauth", "bedrock", "cloudflare":
	default:
		return nil, fmt.Errorf("%w: provider %q has no Chat emulation path", evaluate.ErrUnsupported, req.Provider)
	}
	options := cloneEvaluateEmulationOptions(req.EmulationOptions)
	if options == nil {
		options = &evaluate.EmulationOptions{}
	}
	if defaults := cloneEvaluateEmulationOptions(c.cfg.EvaluateEmulationOptions); defaults != nil {
		if options.MaxTokens == nil {
			options.MaxTokens = defaults.MaxTokens
		}
		if options.ReasoningEffort == nil {
			options.ReasoningEffort = defaults.ReasoningEffort
		}
	}
	req.EmulationOptions = options
	if err := evaluate.ValidateRequest(&req); err != nil {
		return nil, err
	}
	if req.Provider == "openai_codex" && options.MaxTokens != nil {
		return nil, fmt.Errorf("%w: openai_codex does not enforce max_tokens", evaluate.ErrUnsupported)
	}
	if (req.Provider == "azure" || req.Provider == "cloudflare") && options.ReasoningEffort != nil {
		return nil, fmt.Errorf("%w: %s Chat adapter does not map reasoning_effort", evaluate.ErrUnsupported, req.Provider)
	}
	return c.evaluateWithChat(ctx, &req)
}

func cloneEvaluateEmulationOptions(options *evaluate.EmulationOptions) *evaluate.EmulationOptions {
	if options == nil {
		return nil
	}
	out := *options
	if options.ReasoningEffort != nil {
		value := *options.ReasoningEffort
		out.ReasoningEffort = &value
	}
	if options.MaxTokens != nil {
		value := *options.MaxTokens
		out.MaxTokens = &value
	}
	return &out
}
