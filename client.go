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
	openairesp "github.com/quailyquaily/uniai/providers/openai_resp"
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
	userOnStream := req.Options.OnStream
	if userOnStream != nil {
		if len(req.Tools) > 0 && mode != chat.ToolsEmulationOff {
			// Tool emulation owns streaming so it can emit only the final text response
			// and attach aggregated usage/cost for the whole Chat() call.
			req.Options.OnStream = nil
		} else {
			req.Options.OnStream = c.wrapChatStreamCost(providerName, req, userOnStream)
		}
	}
	if len(req.Tools) > 0 && mode == chat.ToolsEmulationForce {
		resp, err := c.chatWithToolEmulation(ctx, providerName, req, nil, userOnStream)
		c.annotateChatResultCost(providerName, req, resp)
		chat.EnsureResultParts(resp)
		return resp, err
	}
	resp, err := c.chatOnce(ctx, providerName, req)
	if err != nil {
		return nil, err
	}
	if len(req.Tools) == 0 {
		c.annotateChatResultCost(providerName, req, resp)
		chat.EnsureResultParts(resp)
		return resp, nil
	}
	if len(resp.ToolCalls) > 0 {
		c.annotateChatResultCost(providerName, req, resp)
		chat.EnsureResultParts(resp)
		return resp, nil
	}
	if mode == chat.ToolsEmulationOff {
		c.annotateChatResultCost(providerName, req, resp)
		chat.EnsureResultParts(resp)
		return resp, nil
	}
	resp, err = c.chatWithToolEmulation(ctx, providerName, req, resp, userOnStream)
	c.annotateChatResultCost(providerName, req, resp)
	chat.EnsureResultParts(resp)
	return resp, err
}

func (c *Client) annotateChatResultCost(providerName string, req *chat.Request, resp *chat.Result) {
	if resp == nil || resp.Usage.Cost != nil || c.cfg.Pricing == nil {
		return
	}
	model := c.resolveChatCostModel(providerName, req, resp)
	if cost, ok := c.estimateChatUsageCost(providerName, req, model, resp.Usage); ok {
		resp.Usage.Cost = cost
	}
}

func (c *Client) wrapChatStreamCost(providerName string, req *chat.Request, onStream chat.OnStreamFunc) chat.OnStreamFunc {
	if onStream == nil {
		return nil
	}
	model := c.resolveChatRequestedModel(providerName, req)
	return func(ev chat.StreamEvent) error {
		if ev.Done && ev.Usage != nil && ev.Usage.Cost == nil && c.cfg.Pricing != nil {
			usage := *ev.Usage
			if cost, ok := c.estimateChatUsageCost(providerName, req, model, usage); ok {
				usage.Cost = cost
				ev.Usage = &usage
			}
		}
		return onStream(ev)
	}
}

func (c *Client) estimateChatUsageCost(providerName string, req *chat.Request, model string, usage chat.Usage) (*chat.UsageCost, bool) {
	if c.cfg.Pricing == nil {
		return nil, false
	}
	if usage.Cost != nil {
		return usage.Cost, true
	}
	if model == "" {
		model = c.resolveChatRequestedModel(providerName, req)
	}
	inferenceProvider := ""
	if req != nil {
		inferenceProvider = req.InferenceProvider
	}
	return c.cfg.Pricing.EstimateChatCostWithInferenceProvider(inferenceProvider, model, usage)
}

func (c *Client) resolveChatCostModel(providerName string, req *chat.Request, resp *chat.Result) string {
	if resp != nil && resp.Model != "" {
		return resp.Model
	}
	return c.resolveChatRequestedModel(providerName, req)
}

func (c *Client) resolveChatRequestedModel(providerName string, req *chat.Request) string {
	if req != nil && req.Model != "" {
		return req.Model
	}
	switch providerName {
	case "gemini":
		if c.cfg.GeminiModel != "" {
			return c.cfg.GeminiModel
		}
		return c.cfg.OpenAIModel
	case "azure":
		return c.cfg.AzureOpenAIModel
	case "anthropic":
		return c.cfg.AnthropicModel
	case "bedrock":
		return c.cfg.AwsBedrockModelArn
	default:
		return c.cfg.OpenAIModel
	}
}

func (c *Client) chatOnce(ctx context.Context, providerName string, req *chat.Request) (*chat.Result, error) {
	switch providerName {
	case "openai", "deepseek", "xai", "groq":
		base := c.cfg.OpenAIAPIBase
		switch providerName {
		case "deepseek":
			base = deepseekAPIBase
		case "xai":
			base = xaiAPIBase
		case "groq":
			base = groqAPIBase
		}

		p, err := openai.New(openai.Config{
			APIKey:       c.cfg.OpenAIAPIKey,
			BaseURL:      base,
			DefaultModel: c.cfg.OpenAIModel,
			Headers:      c.cfg.ChatHeaders,
			Debug:        c.cfg.Debug,
		})
		if err != nil {
			return nil, err
		}
		return p.Chat(ctx, req)

	case "openai_resp":
		p, err := openairesp.New(openairesp.Config{
			APIKey:       c.cfg.OpenAIAPIKey,
			BaseURL:      c.cfg.OpenAIAPIBase,
			DefaultModel: c.cfg.OpenAIModel,
			Headers:      c.cfg.ChatHeaders,
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
			Headers:      c.cfg.ChatHeaders,
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
			Headers:    c.cfg.ChatHeaders,
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
			Headers:      c.cfg.ChatHeaders,
			Debug:        c.cfg.Debug,
		})
		return p.Chat(ctx, req)

	case "bedrock":
		p := bedrock.New(bedrock.Config{
			AwsKey:          c.cfg.AwsKey,
			AwsSecret:       c.cfg.AwsSecret,
			AwsSessionToken: c.cfg.AwsSessionToken,
			AwsRegion:       c.cfg.AwsRegion,
			ModelArn:        c.cfg.AwsBedrockModelArn,
			Headers:         c.cfg.ChatHeaders,
			Debug:           c.cfg.Debug,
		})
		return p.Chat(ctx, req)

	case "cloudflare":
		p, err := cloudflare.New(cloudflare.Config{
			AccountID: c.cfg.CloudflareAccountID,
			APIToken:  c.cfg.CloudflareAPIToken,
			APIBase:   c.cfg.CloudflareAPIBase,
			Headers:   c.cfg.ChatHeaders,
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
