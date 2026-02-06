package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/quailyquaily/uniai/internal/providers/cloudflare"
	"github.com/quailyquaily/uniai/internal/providers/gemini"
	"github.com/quailyquaily/uniai/internal/providers/jina"
	"github.com/quailyquaily/uniai/internal/providers/openai"
)

type Config struct {
	JinaAPIKey          string
	JinaAPIBase         string
	OpenAIAPIKey        string
	OpenAIAPIBase       string
	GeminiAPIKey        string
	GeminiAPIBase       string
	CloudflareAccountID string
	CloudflareAPIToken  string
	CloudflareAPIBase   string
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
	case "jina":
		respData, err = jina.CreateEmbeddings(ctx, c.cfg.JinaAPIKey, c.cfg.JinaAPIBase, req.Model, toJinaInputs(req.Input), req.Options.Jina)
	case "openai":
		respData, err = openai.CreateEmbeddings(ctx, c.cfg.OpenAIAPIKey, c.cfg.OpenAIAPIBase, req.Model, toTextInputs(req.Input), req.Options.OpenAI)
	case "gemini":
		respData, err = gemini.CreateEmbeddings(ctx, c.cfg.GeminiAPIKey, c.cfg.GeminiAPIBase, req.Model, toTextInputs(req.Input), req.Options.Gemini)
	case "cloudflare":
		respData, err = cloudflare.CreateEmbeddings(ctx, c.cfg.CloudflareAPIToken, c.cfg.CloudflareAPIBase, c.cfg.CloudflareAccountID, req.Model, toTextInputs(req.Input), req.Options.Cloudflare)
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
	if strings.Contains(model, "jina") {
		return "jina"
	}
	if strings.Contains(model, "gemini") {
		return "gemini"
	}
	if strings.HasPrefix(model, "@cf/") {
		return "cloudflare"
	}
	return "openai"
}

func toTextInputs(inputs []Input) []string {
	out := make([]string, 0, len(inputs))
	for _, in := range inputs {
		if in.Text != "" {
			out = append(out, in.Text)
		}
	}
	return out
}

func toJinaInputs(inputs []Input) []jina.EmbeddingInput {
	out := make([]jina.EmbeddingInput, 0, len(inputs))
	for _, in := range inputs {
		out = append(out, jina.EmbeddingInput{
			Text:  in.Text,
			Image: in.Image,
		})
	}
	return out
}
