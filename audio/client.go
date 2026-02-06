package audio

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/quailyquaily/uniai/internal/providers/cloudflare"
)

type Config struct {
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
	case "cloudflare":
		respData, err = cloudflare.Transcribe(ctx, c.cfg.CloudflareAPIToken, c.cfg.CloudflareAPIBase, c.cfg.CloudflareAccountID, req.Model, req.Audio, req.Options.Cloudflare)
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
	if strings.HasPrefix(model, "@cf/") {
		return "cloudflare"
	}
	return "cloudflare"
}
