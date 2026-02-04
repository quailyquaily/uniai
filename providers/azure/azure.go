package azure

import (
	"context"
	"fmt"

	"github.com/lyricat/goutils/structs"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/diag"
	"github.com/quailyquaily/uniai/internal/oaicompat"
)

type Config struct {
	APIKey     string
	Endpoint   string
	Deployment string
	APIVersion string
	Debug      bool
}

type Provider struct {
	client     openai.Client
	deployment string
	debug      bool
}

const azureAPIVersion = "2024-08-01-preview"

func New(cfg Config) (*Provider, error) {
	if cfg.APIKey == "" || cfg.Endpoint == "" {
		return nil, fmt.Errorf("azure openai api key and endpoint are required")
	}
	if cfg.Deployment == "" {
		return nil, fmt.Errorf("azure openai deployment is required")
	}
	apiVersion := cfg.APIVersion
	if apiVersion == "" {
		apiVersion = azureAPIVersion
	}
	client := openai.NewClient(
		azure.WithEndpoint(cfg.Endpoint, apiVersion),
		azure.WithAPIKey(cfg.APIKey),
	)
	return &Provider{
		client:     client,
		deployment: cfg.Deployment,
		debug:      cfg.Debug,
	}, nil
}

func (p *Provider) Chat(ctx context.Context, req *chat.Request) (*chat.Result, error) {
	debugFn := req.Options.DebugFn
	messages, err := oaicompat.ToMessages(req.Messages)
	if err != nil {
		return nil, err
	}

	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(p.deployment),
		Messages: messages,
	}

	if req.Options.Temperature != nil {
		params.Temperature = openai.Float(*req.Options.Temperature)
	}
	if req.Options.TopP != nil {
		params.TopP = openai.Float(*req.Options.TopP)
	}
	if req.Options.MaxTokens != nil {
		params.MaxTokens = openai.Int(int64(*req.Options.MaxTokens))
	}
	if len(req.Options.Stop) > 0 {
		params.Stop = openai.ChatCompletionNewParamsStopUnion{OfStringArray: append([]string{}, req.Options.Stop...)}
	}
	if req.Options.PresencePenalty != nil {
		params.PresencePenalty = openai.Float(*req.Options.PresencePenalty)
	}
	if req.Options.FrequencyPenalty != nil {
		params.FrequencyPenalty = openai.Float(*req.Options.FrequencyPenalty)
	}
	if req.Options.User != nil {
		params.User = openai.String(*req.Options.User)
	}

	if len(req.Tools) > 0 {
		tools, err := oaicompat.ToToolParams(req.Tools)
		if err != nil {
			return nil, err
		}
		if len(tools) > 0 {
			params.Tools = tools
		}
	}

	if req.ToolChoice != nil {
		params.ToolChoice = oaicompat.ToToolChoice(req.ToolChoice)
	}

	applyAzureOptions(&params, req.Options.Azure, req.Options.OpenAI)
	diag.LogJSON(p.debug, debugFn, "azure.chat.request", params)

	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, err
	}
	if raw := resp.RawJSON(); raw != "" {
		diag.LogText(p.debug, debugFn, "azure.chat.response", raw)
	} else {
		diag.LogJSON(p.debug, debugFn, "azure.chat.response", resp)
	}

	text := ""
	var toolCalls []chat.ToolCall
	for _, choice := range resp.Choices {
		text += choice.Message.Content
		if len(choice.Message.ToolCalls) > 0 && len(toolCalls) == 0 {
			toolCalls = oaicompat.ToToolCalls(choice.Message.ToolCalls)
		}
	}

	return &chat.Result{
		Text:      text,
		Model:     resp.Model,
		ToolCalls: toolCalls,
		Usage: chat.Usage{
			InputTokens:  int(resp.Usage.PromptTokens),
			OutputTokens: int(resp.Usage.CompletionTokens),
			TotalTokens:  int(resp.Usage.TotalTokens),
		},
		Raw: resp,
	}, nil
}

func applyAzureOptions(params *openai.ChatCompletionNewParams, azureOpts, openaiOpts structs.JSONMap) {
	opts := azureOpts
	if len(opts) == 0 && len(openaiOpts) > 0 {
		opts = openaiOpts
	}
	oaicompat.ApplyOptions(params, opts)
}
