package openai

import (
	"context"
	"encoding/json"
	"time"

	uniai "github.com/quailyquaily/uniai"
	"github.com/quailyquaily/uniai/chat"
	openaiapi "github.com/sashabaranov/go-openai"
)

type Client struct {
	base *uniai.Client
}

func New(client *uniai.Client) *Client {
	return &Client{base: client}
}

func (c *Client) CreateChatCompletion(ctx context.Context, req openaiapi.ChatCompletionRequest) (openaiapi.ChatCompletionResponse, error) {
	opts, err := toChatOptions(req)
	if err != nil {
		return openaiapi.ChatCompletionResponse{}, err
	}
	result, err := c.base.Chat(ctx, opts...)
	if err != nil {
		return openaiapi.ChatCompletionResponse{}, err
	}
	return toOpenAIResponse(result, req.Model), nil
}

func toChatOptions(req openaiapi.ChatCompletionRequest) ([]chat.Option, error) {
	opts := []chat.Option{}
	if req.Model != "" {
		opts = append(opts, chat.WithModel(req.Model))
	}

	if len(req.Messages) > 0 {
		msgs := make([]chat.Message, 0, len(req.Messages))
		for _, m := range req.Messages {
			msg := chat.Message{
				Role:       m.Role,
				Content:    m.Content,
				Name:       m.Name,
				ToolCallID: m.ToolCallID,
			}
			if len(m.ToolCalls) > 0 {
				msg.ToolCalls = make([]chat.ToolCall, 0, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					msg.ToolCalls = append(msg.ToolCalls, chat.ToolCall{
						ID:   tc.ID,
						Type: string(tc.Type),
						Function: chat.ToolCallFunction{
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
						},
					})
				}
			}
			msgs = append(msgs, msg)
		}
		opts = append(opts, chat.WithMessages(msgs...))
	}

	if req.Temperature != 0 {
		opts = append(opts, chat.WithTemperature(float64(req.Temperature)))
	}
	if req.TopP != 0 {
		opts = append(opts, chat.WithTopP(float64(req.TopP)))
	}
	if req.MaxTokens > 0 {
		opts = append(opts, chat.WithMaxTokens(req.MaxTokens))
	} else if req.MaxCompletionTokens > 0 {
		opts = append(opts, chat.WithMaxTokens(req.MaxCompletionTokens))
	}
	if len(req.Stop) > 0 {
		opts = append(opts, chat.WithStopWords(req.Stop...))
	}
	if req.PresencePenalty != 0 {
		opts = append(opts, chat.WithPresencePenalty(float64(req.PresencePenalty)))
	}
	if req.FrequencyPenalty != 0 {
		opts = append(opts, chat.WithFrequencyPenalty(float64(req.FrequencyPenalty)))
	}
	if req.User != "" {
		opts = append(opts, chat.WithUser(req.User))
	}

	if len(req.Tools) > 0 {
		tools := make([]chat.Tool, 0, len(req.Tools))
		for _, t := range req.Tools {
			if t.Type != openaiapi.ToolTypeFunction || t.Function == nil {
				continue
			}
			tools = append(tools, chat.Tool{
				Type: "function",
				Function: chat.ToolFunction{
					Name:                 t.Function.Name,
					Description:          t.Function.Description,
					ParametersJSONSchema: toJSONBytes(t.Function.Parameters),
					Strict:               boolPtr(t.Function.Strict),
				},
			})
		}
		opts = append(opts, chat.WithTools(tools))
	}

	if req.ToolChoice != nil {
		switch v := req.ToolChoice.(type) {
		case string:
			switch v {
			case "auto":
				opts = append(opts, chat.WithToolChoice(chat.ToolChoiceAuto()))
			case "none":
				opts = append(opts, chat.WithToolChoice(chat.ToolChoiceNone()))
			case "required":
				opts = append(opts, chat.WithToolChoice(chat.ToolChoiceRequired()))
			}
		case openaiapi.ToolChoice:
			if v.Type == openaiapi.ToolTypeFunction {
				opts = append(opts, chat.WithToolChoice(chat.ToolChoiceFunction(v.Function.Name)))
			}
		case *openaiapi.ToolChoice:
			if v != nil && v.Type == openaiapi.ToolTypeFunction {
				opts = append(opts, chat.WithToolChoice(chat.ToolChoiceFunction(v.Function.Name)))
			}
		}
	}

	return opts, nil
}

func toOpenAIResponse(result *chat.Result, model string) openaiapi.ChatCompletionResponse {
	msg := openaiapi.ChatCompletionMessage{
		Role:    openaiapi.ChatMessageRoleAssistant,
		Content: result.Text,
	}
	if len(result.ToolCalls) > 0 {
		msg.ToolCalls = make([]openaiapi.ToolCall, 0, len(result.ToolCalls))
		for _, tc := range result.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, openaiapi.ToolCall{
				ID:   tc.ID,
				Type: openaiapi.ToolType(tc.Type),
				Function: openaiapi.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	}

	resp := openaiapi.ChatCompletionResponse{
		ID:      "",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []openaiapi.ChatCompletionChoice{{
			Index:        0,
			Message:      msg,
			FinishReason: finishReason(result),
		}},
		Usage: openaiapi.Usage{
			PromptTokens:     result.Usage.InputTokens,
			CompletionTokens: result.Usage.OutputTokens,
			TotalTokens:      result.Usage.TotalTokens,
		},
	}
	if result.Model != "" {
		resp.Model = result.Model
	}
	return resp
}

func finishReason(result *chat.Result) openaiapi.FinishReason {
	if result != nil && len(result.ToolCalls) > 0 {
		return openaiapi.FinishReasonToolCalls
	}
	return openaiapi.FinishReasonStop
}

func toJSONBytes(v any) []byte {
	if v == nil {
		return nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw
	}
	if b, ok := v.([]byte); ok {
		return b
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}

func boolPtr(v bool) *bool {
	return &v
}
