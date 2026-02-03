package uniai

import (
	"context"
	"fmt"
	"strings"

	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/classify"
	"github.com/quailyquaily/uniai/embedding"
	"github.com/quailyquaily/uniai/image"
	"github.com/quailyquaily/uniai/providers/anthropic"
	"github.com/quailyquaily/uniai/providers/azure"
	"github.com/quailyquaily/uniai/providers/bedrock"
	"github.com/quailyquaily/uniai/providers/openai"
	"github.com/quailyquaily/uniai/providers/susanoo"
	"github.com/quailyquaily/uniai/rerank"
)

type Client struct {
	cfg Config

	embeddingClient *embedding.Client
	imageClient     *image.Client
	rerankClient    *rerank.Client
	classifyClient  *classify.Client
}

func New(cfg Config) *Client {
	cfg = cfg.withDefaults()
	return &Client{
		cfg: cfg,
		embeddingClient: embedding.New(embedding.Config{
			JinaAPIKey:    cfg.JinaAPIKey,
			JinaAPIBase:   cfg.JinaAPIBase,
			OpenAIAPIKey:  cfg.OpenAIAPIKey,
			OpenAIAPIBase: cfg.OpenAIAPIBase,
			GeminiAPIKey:  cfg.GeminiAPIKey,
			GeminiAPIBase: cfg.GeminiAPIBase,
		}),
		imageClient: image.New(image.Config{
			OpenAIAPIKey:  cfg.OpenAIAPIKey,
			OpenAIAPIBase: cfg.OpenAIAPIBase,
			GeminiAPIKey:  cfg.GeminiAPIKey,
		}),
		rerankClient: rerank.New(rerank.Config{
			JinaAPIKey:  cfg.JinaAPIKey,
			JinaAPIBase: cfg.JinaAPIBase,
		}),
		classifyClient: classify.New(classify.Config{
			JinaAPIKey:  cfg.JinaAPIKey,
			JinaAPIBase: cfg.JinaAPIBase,
		}),
	}
}

func (c *Client) Chat(ctx context.Context, opts ...chat.Option) (*chat.Result, error) {
	req, err := chat.BuildRequest(opts...)
	if err != nil {
		return nil, err
	}

	providerName := req.Provider
	if providerName == "" {
		providerName = c.cfg.Provider
	}
	if providerName == "" {
		providerName = "openai"
	}

	switch providerName {
	case "openai", "openai_custom", "deepseek", "xai":
		base := c.cfg.OpenAIAPIBase
		switch providerName {
		case "deepseek":
			base = "https://api.deepseek.com"
		case "xai":
			base = "https://api.x.ai/v1"
		case "openai_custom":
			// keep cfg.OpenAIAPIBase
		}

		p, err := openai.New(openai.Config{
			APIKey:       c.cfg.OpenAIAPIKey,
			BaseURL:      base,
			DefaultModel: c.cfg.OpenAIModel,
		})
		if err != nil {
			return nil, err
		}
		return p.Chat(ctx, req)

	case "gemini":
		base := strings.TrimRight(c.cfg.GeminiAPIBase, "/")
		if base == "" {
			base = DefaultGeminiAPIBase
		}
		if strings.HasSuffix(base, "/v1beta") {
			base += "/openai"
		} else if !strings.Contains(base, "/openai") {
			base += "/v1beta/openai"
		}
		apiKey := c.cfg.GeminiAPIKey
		if apiKey == "" {
			apiKey = c.cfg.OpenAIAPIKey
		}
		p, err := openai.New(openai.Config{
			APIKey:       apiKey,
			BaseURL:      base,
			DefaultModel: c.cfg.OpenAIModel,
		})
		if err != nil {
			return nil, err
		}
		return p.Chat(ctx, req)

	case "azure":
		p, err := azure.New(azure.Config{
			APIKey:     c.cfg.AzureOpenAIAPIKey,
			Endpoint:   c.cfg.AzureOpenAIEndpoint,
			Deployment: c.cfg.AzureOpenAIModel,
		})
		if err != nil {
			return nil, err
		}
		return p.Chat(ctx, req)

	case "anthropic":
		p := anthropic.New(anthropic.Config{
			APIKey:       c.cfg.AnthropicAPIKey,
			DefaultModel: c.cfg.AnthropicModel,
		})
		return p.Chat(ctx, req)

	case "bedrock":
		p := bedrock.New(bedrock.Config{
			AwsKey:    c.cfg.AwsKey,
			AwsSecret: c.cfg.AwsSecret,
			AwsRegion: c.cfg.AwsRegion,
			ModelArn:  c.cfg.AwsBedrockModelArn,
		})
		return p.Chat(ctx, req)

	case "susanoo":
		p := susanoo.New(susanoo.Config{
			APIBase: c.cfg.SusanooAPIBase,
			APIKey:  c.cfg.SusanooAPIKey,
		})
		return p.Chat(ctx, req)

	default:
		return nil, fmt.Errorf("provider %s not supported", providerName)
	}
}

func (c *Client) Embedding(ctx context.Context, opts ...embedding.Option) (*embedding.Result, error) {
	if c.embeddingClient == nil {
		return nil, fmt.Errorf("embedding client not configured")
	}
	return c.embeddingClient.Create(ctx, opts...)
}

func (c *Client) Image(ctx context.Context, opts ...image.Option) (*image.Result, error) {
	if c.imageClient == nil {
		return nil, fmt.Errorf("image client not configured")
	}
	return c.imageClient.Create(ctx, opts...)
}

func (c *Client) Rerank(ctx context.Context, opts ...rerank.Option) (*rerank.Result, error) {
	if c.rerankClient == nil {
		return nil, fmt.Errorf("rerank client not configured")
	}
	return c.rerankClient.Rerank(ctx, opts...)
}

func (c *Client) Classify(ctx context.Context, opts ...classify.Option) (*classify.Result, error) {
	if c.classifyClient == nil {
		return nil, fmt.Errorf("classify client not configured")
	}
	return c.classifyClient.Classify(ctx, opts...)
}
