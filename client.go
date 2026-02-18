package uniai

import (
	"context"
	"fmt"

	"github.com/quailyquaily/uniai/audio"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/classify"
	"github.com/quailyquaily/uniai/embedding"
	"github.com/quailyquaily/uniai/image"
	"github.com/quailyquaily/uniai/providers/anthropic"
	"github.com/quailyquaily/uniai/providers/azure"
	"github.com/quailyquaily/uniai/providers/bedrock"
	"github.com/quailyquaily/uniai/providers/cloudflare"
	"github.com/quailyquaily/uniai/providers/gemini"
	"github.com/quailyquaily/uniai/providers/openai"
	"github.com/quailyquaily/uniai/rerank"
)

type Client struct {
	cfg Config

	embeddingClient *embedding.Client
	imageClient     *image.Client
	rerankClient    *rerank.Client
	classifyClient  *classify.Client
	audioClient     *audio.Client
}

func New(cfg Config) *Client {
	cfg = cfg.withDefaults()
	return &Client{
		cfg: cfg,
		embeddingClient: embedding.New(embedding.Config{
			JinaAPIKey:          cfg.JinaAPIKey,
			JinaAPIBase:         cfg.JinaAPIBase,
			OpenAIAPIKey:        cfg.OpenAIAPIKey,
			OpenAIAPIBase:       cfg.OpenAIAPIBase,
			GeminiAPIKey:        cfg.GeminiAPIKey,
			GeminiAPIBase:       cfg.GeminiAPIBase,
			CloudflareAccountID: cfg.CloudflareAccountID,
			CloudflareAPIToken:  cfg.CloudflareAPIToken,
			CloudflareAPIBase:   cfg.CloudflareAPIBase,
		}),
		imageClient: image.New(image.Config{
			OpenAIAPIKey:        cfg.OpenAIAPIKey,
			OpenAIAPIBase:       cfg.OpenAIAPIBase,
			GeminiAPIKey:        cfg.GeminiAPIKey,
			CloudflareAccountID: cfg.CloudflareAccountID,
			CloudflareAPIToken:  cfg.CloudflareAPIToken,
			CloudflareAPIBase:   cfg.CloudflareAPIBase,
		}),
		rerankClient: rerank.New(rerank.Config{
			JinaAPIKey:  cfg.JinaAPIKey,
			JinaAPIBase: cfg.JinaAPIBase,
		}),
		classifyClient: classify.New(classify.Config{
			JinaAPIKey:  cfg.JinaAPIKey,
			JinaAPIBase: cfg.JinaAPIBase,
		}),
		audioClient: audio.New(audio.Config{
			CloudflareAccountID: cfg.CloudflareAccountID,
			CloudflareAPIToken:  cfg.CloudflareAPIToken,
			CloudflareAPIBase:   cfg.CloudflareAPIBase,
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
	mode := req.Options.ToolsEmulationMode
	if mode == "" {
		mode = chat.ToolsEmulationOff
	}
	if len(req.Tools) > 0 && mode == chat.ToolsEmulationForce {
		resp, err := c.chatWithToolEmulation(ctx, providerName, req)
		chat.EnsureResultParts(resp)
		return resp, err
	}
	resp, err := c.chatOnce(ctx, providerName, req)
	if err != nil {
		return nil, err
	}
	chat.EnsureResultParts(resp)
	if len(req.Tools) == 0 {
		return resp, nil
	}
	if len(resp.ToolCalls) > 0 {
		return resp, nil
	}
	if mode == chat.ToolsEmulationOff {
		return resp, nil
	}
	resp, err = c.chatWithToolEmulation(ctx, providerName, req)
	chat.EnsureResultParts(resp)
	return resp, err
}

func (c *Client) chatOnce(ctx context.Context, providerName string, req *chat.Request) (*chat.Result, error) {
	switch providerName {
	case "openai", "openai_custom", "deepseek", "xai", "groq":
		base := c.cfg.OpenAIAPIBase
		switch providerName {
		case "deepseek":
			base = deepseekAPIBase
		case "xai":
			base = xaiAPIBase
		case "groq":
			base = groqAPIBase
		case "openai_custom":
			// keep cfg.OpenAIAPIBase
		}

		p, err := openai.New(openai.Config{
			APIKey:       c.cfg.OpenAIAPIKey,
			BaseURL:      base,
			DefaultModel: c.cfg.OpenAIModel,
			Debug:        c.cfg.Debug,
		})
		if err != nil {
			return nil, err
		}
		return p.Chat(ctx, req)

	case "gemini":
		apiKey := c.cfg.GeminiAPIKey
		if apiKey == "" {
			apiKey = c.cfg.OpenAIAPIKey
		}
		geminiModel := c.cfg.GeminiModel
		if geminiModel == "" {
			geminiModel = c.cfg.OpenAIModel
		}
		p, err := gemini.New(gemini.Config{
			APIKey:       apiKey,
			BaseURL:      c.cfg.GeminiAPIBase,
			DefaultModel: geminiModel,
			Debug:        c.cfg.Debug,
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
			APIVersion: c.cfg.AzureOpenAIAPIVersion,
			Debug:      c.cfg.Debug,
		})
		if err != nil {
			return nil, err
		}
		return p.Chat(ctx, req)

	case "anthropic":
		p := anthropic.New(anthropic.Config{
			APIKey:       c.cfg.AnthropicAPIKey,
			DefaultModel: c.cfg.AnthropicModel,
			Debug:        c.cfg.Debug,
		})
		return p.Chat(ctx, req)

	case "bedrock":
		p := bedrock.New(bedrock.Config{
			AwsKey:    c.cfg.AwsKey,
			AwsSecret: c.cfg.AwsSecret,
			AwsRegion: c.cfg.AwsRegion,
			ModelArn:  c.cfg.AwsBedrockModelArn,
			Debug:     c.cfg.Debug,
		})
		return p.Chat(ctx, req)

	case "cloudflare":
		p, err := cloudflare.New(cloudflare.Config{
			AccountID: c.cfg.CloudflareAccountID,
			APIToken:  c.cfg.CloudflareAPIToken,
			APIBase:   c.cfg.CloudflareAPIBase,
			Debug:     c.cfg.Debug,
		})
		if err != nil {
			return nil, err
		}
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

func (c *Client) Audio(ctx context.Context, opts ...audio.Option) (*audio.Result, error) {
	if c.audioClient == nil {
		return nil, fmt.Errorf("audio client not configured")
	}
	return c.audioClient.Create(ctx, opts...)
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
