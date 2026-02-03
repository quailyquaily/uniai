package classify

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

func (c *Client) Classify(ctx context.Context, opts ...Option) (*Result, error) {
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
		respData, err = jina.Classify(ctx, c.cfg.JinaAPIKey, c.cfg.JinaAPIBase, req.Model, req.Labels, toJinaInputs(req.Input))
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

func toJinaInputs(inputs []Input) []jina.ClassifyInput {
	out := make([]jina.ClassifyInput, 0, len(inputs))
	for _, in := range inputs {
		out = append(out, jina.ClassifyInput{
			Text:  in.Text,
			Image: in.Image,
		})
	}
	return out
}
