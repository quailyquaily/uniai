package rerank

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/quailyquaily/uniai/internal/providers/jina"
)

type Config struct {
	JinaAPIKey  string
	JinaAPIBase string
}

type Client struct {
	cfg Config
}

func New(cfg Config) *Client {
	return &Client{cfg: cfg}
}

func (c *Client) Rerank(ctx context.Context, opts ...Option) (*Result, error) {
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
		respData, err = jina.Rerank(ctx, c.cfg.JinaAPIKey, c.cfg.JinaAPIBase, req.Model, req.Query, toJinaDocs(req.Documents), req.TopN, req.ReturnDocuments)
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
	return ""
}

func toJinaDocs(docs []Input) []jina.RerankInput {
	out := make([]jina.RerankInput, 0, len(docs))
	for _, d := range docs {
		out = append(out, jina.RerankInput{
			Text:  d.Text,
			Image: d.Image,
		})
	}
	return out
}
