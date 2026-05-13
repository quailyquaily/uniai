package image

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/quailyquaily/uniai/internal/providers/cloudflare"
	"github.com/quailyquaily/uniai/internal/providers/gemini"
	"github.com/quailyquaily/uniai/internal/providers/openai"
)

type Config struct {
	OpenAIAPIKey        string
	OpenAIAPIBase       string
	GeminiAPIKey        string
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
		rawData  []byte
		err      error
	)
	switch provider {
	case "openai":
		respData, rawData, err = openai.CreateImages(ctx, c.cfg.OpenAIAPIKey, c.cfg.OpenAIAPIBase, req.Model, req.Prompt, req.Count, req.Options.OpenAI)
	case "gemini":
		respData, rawData, err = gemini.CreateImages(ctx, c.cfg.GeminiAPIKey, req.Model, req.Prompt, req.Count, req.Options.Gemini)
	case "cloudflare":
		respData, rawData, err = cloudflare.CreateImages(ctx, c.cfg.CloudflareAPIToken, c.cfg.CloudflareAPIBase, c.cfg.CloudflareAccountID, req.Model, req.Prompt, req.Count, req.Options.Cloudflare)
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
	NormalizeResult(&out)
	if len(rawData) > 0 {
		out.Raw = json.RawMessage(append([]byte(nil), rawData...))
	}
	return &out, nil
}

func (c *Client) Edit(ctx context.Context, opts ...ImageEditOption) (*Result, error) {
	req := BuildEditRequest(opts...)
	provider := req.Provider
	if provider == "" {
		provider = pickProviderByModel(req.Model)
	}
	if provider == "" {
		return nil, fmt.Errorf("provider not set")
	}

	var (
		respData []byte
		rawData  []byte
		err      error
	)
	switch provider {
	case "openai":
		respData, rawData, err = openai.EditImages(ctx, c.cfg.OpenAIAPIKey, c.cfg.OpenAIAPIBase, req.Model, req.Prompt, toOpenAIInputImages(req.Images), req.Count, req.Options.OpenAI)
	case "gemini":
		respData, rawData, err = gemini.EditImages(ctx, c.cfg.GeminiAPIKey, req.Model, req.Prompt, toGeminiInputImages(req.Images), req.Count, req.Options.Gemini)
	case "cloudflare":
		return nil, fmt.Errorf("cloudflare image edit is not supported")
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
	NormalizeResult(&out)
	if len(rawData) > 0 {
		out.Raw = json.RawMessage(append([]byte(nil), rawData...))
	}
	return &out, nil
}

func pickProviderByModel(model string) string {
	model = NormalizeModelAlias(strings.TrimSpace(model))
	if strings.HasPrefix(model, "gemini-") || strings.HasPrefix(model, "imagen-") {
		return "gemini"
	}
	if strings.Contains(model, "gpt-") {
		return "openai"
	}
	if strings.HasPrefix(model, "@cf/") {
		return "cloudflare"
	}
	return "openai"
}

func toOpenAIInputImages(images []InputImage) []openai.InputImage {
	out := make([]openai.InputImage, 0, len(images))
	for _, image := range images {
		out = append(out, openai.InputImage{
			Filename: image.Filename,
			MIMEType: image.MIMEType,
			Data:     append([]byte(nil), image.Data...),
		})
	}
	return out
}

func toGeminiInputImages(images []InputImage) []gemini.InputImage {
	out := make([]gemini.InputImage, 0, len(images))
	for _, image := range images {
		out = append(out, gemini.InputImage{
			Filename: image.Filename,
			MIMEType: image.MIMEType,
			Data:     append([]byte(nil), image.Data...),
		})
	}
	return out
}
