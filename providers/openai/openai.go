package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/quailyquaily/uniai/chat"
	openai "github.com/sashabaranov/go-openai"
)

type Config struct {
	APIKey       string
	BaseURL      string
	DefaultModel string
}

type Provider struct {
	client       *openai.Client
	defaultModel string
}

func New(cfg Config) (*Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("openai api key is required")
	}
	clientCfg := openai.DefaultConfig(cfg.APIKey)
	if cfg.BaseURL != "" {
		clientCfg.BaseURL = cfg.BaseURL
	}
	return &Provider{
		client:       openai.NewClientWithConfig(clientCfg),
		defaultModel: cfg.DefaultModel,
	}, nil
}

func (p *Provider) Chat(ctx context.Context, req *chat.Request) (*chat.Result, error) {
	payload, err := buildRequest(req, p.defaultModel)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.CreateChatCompletion(ctx, payload)
	if err != nil {
		return nil, err
	}
	return toResult(resp), nil
}

func buildRequest(req *chat.Request, defaultModel string) (openai.ChatCompletionRequest, error) {
	model := req.Model
	if model == "" {
		model = defaultModel
	}
	if model == "" {
		return openai.ChatCompletionRequest{}, fmt.Errorf("model is required")
	}

	messages := make([]openai.ChatCompletionMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msg := openai.ChatCompletionMessage{
			Role:       m.Role,
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		if len(m.ToolCalls) > 0 {
			msg.ToolCalls = make([]openai.ToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolType(tc.Type),
					Function: openai.FunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		}
		messages = append(messages, msg)
	}

	payload := openai.ChatCompletionRequest{
		Model:    model,
		Messages: messages,
	}

	if req.Options.Temperature != nil {
		payload.Temperature = float32(*req.Options.Temperature)
	}
	if req.Options.TopP != nil {
		payload.TopP = float32(*req.Options.TopP)
	}
	if req.Options.MaxTokens != nil {
		if useMaxCompletionTokens(model) {
			payload.MaxCompletionTokens = *req.Options.MaxTokens
		} else {
			payload.MaxTokens = *req.Options.MaxTokens
		}
	}
	if len(req.Options.Stop) > 0 {
		payload.Stop = append([]string{}, req.Options.Stop...)
	}
	if req.Options.PresencePenalty != nil {
		payload.PresencePenalty = float32(*req.Options.PresencePenalty)
	}
	if req.Options.FrequencyPenalty != nil {
		payload.FrequencyPenalty = float32(*req.Options.FrequencyPenalty)
	}
	if req.Options.User != nil {
		payload.User = *req.Options.User
	}

	if len(req.Tools) > 0 {
		payload.Tools = make([]openai.Tool, 0, len(req.Tools))
		for _, tool := range req.Tools {
			if tool.Type != "function" {
				continue
			}
			def := &openai.FunctionDefinition{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  json.RawMessage(tool.Function.ParametersJSONSchema),
			}
			if tool.Function.Strict != nil {
				def.Strict = *tool.Function.Strict
			}
			payload.Tools = append(payload.Tools, openai.Tool{
				Type:     openai.ToolTypeFunction,
				Function: def,
			})
		}
	}

	if req.ToolChoice != nil {
		switch req.ToolChoice.Mode {
		case "auto", "none", "required":
			payload.ToolChoice = req.ToolChoice.Mode
		case "function":
			payload.ToolChoice = openai.ToolChoice{
				Type: openai.ToolTypeFunction,
				Function: openai.ToolFunction{
					Name: req.ToolChoice.FunctionName,
				},
			}
		}
	}

	return payload, nil
}

func toResult(resp openai.ChatCompletionResponse) *chat.Result {
	text := ""
	var toolCalls []chat.ToolCall
	for _, choice := range resp.Choices {
		text += choice.Message.Content
		if len(choice.Message.ToolCalls) > 0 && len(toolCalls) == 0 {
			for _, tc := range choice.Message.ToolCalls {
				toolCalls = append(toolCalls, chat.ToolCall{
					ID:   tc.ID,
					Type: string(tc.Type),
					Function: chat.ToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		}
	}

	return &chat.Result{
		Text:      text,
		Model:     resp.Model,
		ToolCalls: toolCalls,
		Usage: chat.Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
			TotalTokens:  resp.Usage.TotalTokens,
		},
		Raw: resp,
	}
}

func useMaxCompletionTokens(model string) bool {
	model = strings.ToLower(model)
	return strings.HasPrefix(model, "o1") ||
		strings.HasPrefix(model, "o3") ||
		strings.HasPrefix(model, "o4")
}
