package azure

import (
	"context"
	"encoding/json"
	"fmt"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/openai/openai-go/v3/shared"
	"github.com/quailyquaily/uniai/chat"
)

type Config struct {
	APIKey     string
	Endpoint   string
	Deployment string
}

type Provider struct {
	client     openai.Client
	deployment string
}

const azureAPIVersion = "2024-08-01-preview"

func New(cfg Config) (*Provider, error) {
	if cfg.APIKey == "" || cfg.Endpoint == "" {
		return nil, fmt.Errorf("azure openai api key and endpoint are required")
	}
	if cfg.Deployment == "" {
		return nil, fmt.Errorf("azure openai deployment is required")
	}
	client := openai.NewClient(
		azure.WithEndpoint(cfg.Endpoint, azureAPIVersion),
		azure.WithAPIKey(cfg.APIKey),
	)
	return &Provider{
		client:     client,
		deployment: cfg.Deployment,
	}, nil
}

func (p *Provider) Chat(ctx context.Context, req *chat.Request) (*chat.Result, error) {
	messages, err := toMessages(req.Messages)
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
		tools, err := toToolParams(req.Tools)
		if err != nil {
			return nil, err
		}
		if len(tools) > 0 {
			params.Tools = tools
		}
	}

	if req.ToolChoice != nil {
		params.ToolChoice = toToolChoice(req.ToolChoice)
	}

	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, err
	}

	text := ""
	var toolCalls []chat.ToolCall
	for _, choice := range resp.Choices {
		text += choice.Message.Content
		if len(choice.Message.ToolCalls) > 0 && len(toolCalls) == 0 {
			toolCalls = toToolCalls(choice.Message.ToolCalls)
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

func toMessages(input []chat.Message) ([]openai.ChatCompletionMessageParamUnion, error) {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(input))
	for _, m := range input {
		switch m.Role {
		case chat.RoleSystem:
			msg := openai.ChatCompletionSystemMessageParam{
				Content: openai.ChatCompletionSystemMessageParamContentUnion{OfString: openai.String(m.Content)},
			}
			if m.Name != "" {
				msg.Name = openai.String(m.Name)
			}
			out = append(out, openai.ChatCompletionMessageParamUnion{OfSystem: &msg})
		case chat.RoleUser:
			msg := openai.ChatCompletionUserMessageParam{
				Content: openai.ChatCompletionUserMessageParamContentUnion{OfString: openai.String(m.Content)},
			}
			if m.Name != "" {
				msg.Name = openai.String(m.Name)
			}
			out = append(out, openai.ChatCompletionMessageParamUnion{OfUser: &msg})
		case chat.RoleAssistant:
			msg := openai.ChatCompletionAssistantMessageParam{}
			if m.Content != "" {
				msg.Content = openai.ChatCompletionAssistantMessageParamContentUnion{OfString: openai.String(m.Content)}
			}
			if m.Name != "" {
				msg.Name = openai.String(m.Name)
			}
			if len(m.ToolCalls) > 0 {
				msg.ToolCalls = toToolCallParams(m.ToolCalls)
			}
			out = append(out, openai.ChatCompletionMessageParamUnion{OfAssistant: &msg})
		case chat.RoleTool:
			if m.ToolCallID == "" {
				return nil, fmt.Errorf("tool_call_id is required for tool messages")
			}
			out = append(out, openai.ToolMessage(m.Content, m.ToolCallID))
		default:
			out = append(out, openai.UserMessage(m.Content))
		}
	}
	return out, nil
}

func toToolParams(tools []chat.Tool) ([]openai.ChatCompletionToolUnionParam, error) {
	out := make([]openai.ChatCompletionToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "function" {
			continue
		}
		fn := shared.FunctionDefinitionParam{
			Name: tool.Function.Name,
		}
		if tool.Function.Description != "" {
			fn.Description = openai.String(tool.Function.Description)
		}
		if tool.Function.Strict != nil {
			fn.Strict = openai.Bool(*tool.Function.Strict)
		}
		if len(tool.Function.ParametersJSONSchema) > 0 {
			var params map[string]any
			if err := json.Unmarshal(tool.Function.ParametersJSONSchema, &params); err != nil {
				return nil, err
			}
			fn.Parameters = shared.FunctionParameters(params)
		}
		out = append(out, openai.ChatCompletionFunctionTool(fn))
	}
	return out, nil
}

func toToolChoice(choice *chat.ToolChoice) openai.ChatCompletionToolChoiceOptionUnionParam {
	switch choice.Mode {
	case "none":
		return openai.ChatCompletionToolChoiceOptionUnionParam{
			OfAuto: openai.String(string(openai.ChatCompletionToolChoiceOptionAutoNone)),
		}
	case "required":
		return openai.ChatCompletionToolChoiceOptionUnionParam{
			OfAuto: openai.String(string(openai.ChatCompletionToolChoiceOptionAutoRequired)),
		}
	case "function":
		return openai.ToolChoiceOptionFunctionToolChoice(openai.ChatCompletionNamedToolChoiceFunctionParam{
			Name: choice.FunctionName,
		})
	default:
		return openai.ChatCompletionToolChoiceOptionUnionParam{
			OfAuto: openai.String(string(openai.ChatCompletionToolChoiceOptionAutoAuto)),
		}
	}
}

func toToolCallParams(calls []chat.ToolCall) []openai.ChatCompletionMessageToolCallUnionParam {
	out := make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(calls))
	for _, call := range calls {
		if call.Type != "" && call.Type != "function" {
			continue
		}
		if call.ID == "" || call.Function.Name == "" {
			continue
		}
		out = append(out, openai.ChatCompletionMessageToolCallUnionParam{
			OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
				ID: call.ID,
				Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				},
			},
		})
	}
	return out
}

func toToolCalls(calls []openai.ChatCompletionMessageToolCallUnion) []chat.ToolCall {
	out := make([]chat.ToolCall, 0, len(calls))
	for _, call := range calls {
		if call.Type != "function" {
			continue
		}
		if call.Function.Name == "" {
			continue
		}
		out = append(out, chat.ToolCall{
			ID:   call.ID,
			Type: call.Type,
			Function: chat.ToolCallFunction{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	return out
}
