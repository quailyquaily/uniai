package image

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/quailyquaily/uniai/internal/providers/gemini"
	"github.com/quailyquaily/uniai/internal/providers/openai"
)

type Config struct {
	OpenAIAPIKey  string
	OpenAIAPIBase string
	GeminiAPIKey  string
}

type Client struct {
	cfg Config
}

func New(cfg Config) *Client {
	return &Client{cfg: cfg}
}

func (c *Client) Create(ctx context.Context, opts ...Option) (*Result, error) {
	req := BuildRequest(opts...)
	provider := req.Provider
	if provider == "" {
		provider = pickProviderByModel(req.Model)
	}
	if provider == "" {
		return nil, fmt.Errorf("provider not set")
	}

	var (
		respData []byte
		err      error
	)
	switch provider {
	case "openai", "openai_custom":
		respData, err = openai.CreateImages(ctx, c.cfg.OpenAIAPIKey, c.cfg.OpenAIAPIBase, req.Model, req.Prompt, req.Count, req.Options.OpenAI)
	case "gemini":
		respData, err = gemini.CreateImages(ctx, c.cfg.GeminiAPIKey, req.Model, req.Prompt, req.Count, req.Options.Gemini)
	default:
		return nil, fmt.Errorf("unknown provider: %s", provider)
	}
	if err != nil {
		return nil, err
	}

	var out Result
	if err := json.Unmarshal(respData, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func pickProviderByModel(model string) string {
	if strings.HasPrefix(model, "gemini-") || strings.HasPrefix(model, "imagen-") {
		return "gemini"
	}
	if strings.Contains(model, "gpt-") {
		return "openai"
	}
	return "openai"
}
