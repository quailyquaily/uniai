package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lyricat/goutils/structs"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared/constant"
	uniai "github.com/quailyquaily/uniai"
	"github.com/quailyquaily/uniai/chat"
)

type Client struct {
	base *uniai.Client
}

func New(client *uniai.Client) *Client {
	return &Client{base: client}
}

func (c *Client) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionNewParams) (openai.ChatCompletion, error) {
	opts, err := toChatOptions(req)
	if err != nil {
		return openai.ChatCompletion{}, err
	}
	result, err := c.base.Chat(ctx, opts...)
	if err != nil {
		return openai.ChatCompletion{}, err
	}
	return toOpenAIResponse(result, string(req.Model)), nil
}

func toChatOptions(req openai.ChatCompletionNewParams) ([]chat.Option, error) {
	opts := []chat.Option{}
	if req.Model != "" {
		opts = append(opts, chat.WithModel(string(req.Model)))
	}

	if len(req.Messages) > 0 {
		msgs := make([]chat.Message, 0, len(req.Messages))
		for _, m := range req.Messages {
			msg, err := toChatMessage(m)
			if err != nil {
				return nil, err
			}
			msgs = append(msgs, msg)
		}
		opts = append(opts, chat.WithMessages(msgs...))
	}

	if req.Temperature.Valid() {
		opts = append(opts, chat.WithTemperature(req.Temperature.Value))
	}
	if req.TopP.Valid() {
		opts = append(opts, chat.WithTopP(req.TopP.Value))
	}
	if req.MaxCompletionTokens.Valid() {
		opts = append(opts, chat.WithMaxTokens(int(req.MaxCompletionTokens.Value)))
	} else if req.MaxTokens.Valid() {
		opts = append(opts, chat.WithMaxTokens(int(req.MaxTokens.Value)))
	}
	if len(req.Stop.OfStringArray) > 0 {
		opts = append(opts, chat.WithStopWords(req.Stop.OfStringArray...))
	} else if req.Stop.OfString.Valid() {
		opts = append(opts, chat.WithStopWords(req.Stop.OfString.Value))
	}
	if req.PresencePenalty.Valid() {
		opts = append(opts, chat.WithPresencePenalty(req.PresencePenalty.Value))
	}
	if req.FrequencyPenalty.Valid() {
		opts = append(opts, chat.WithFrequencyPenalty(req.FrequencyPenalty.Value))
	}
	if req.User.Valid() {
		opts = append(opts, chat.WithUser(req.User.Value))
	}

	if len(req.Tools) > 0 {
		tools, err := toTools(req.Tools)
		if err != nil {
			return nil, err
		}
		if len(tools) > 0 {
			opts = append(opts, chat.WithTools(tools))
		}
	}

	if choice, ok := toToolChoice(req.ToolChoice); ok {
		opts = append(opts, chat.WithToolChoice(choice))
	}

	if extra := toOpenAIOptions(req); len(extra) > 0 {
		opts = append(opts, chat.WithOpenAIOptions(extra))
	}

	return opts, nil
}

func toChatMessage(m openai.ChatCompletionMessageParamUnion) (chat.Message, error) {
	switch {
	case m.OfDeveloper != nil:
		content, err := readTextFromDeveloper(m.OfDeveloper.Content)
		if err != nil {
			return chat.Message{}, err
		}
		return chat.Message{Role: chat.RoleSystem, Content: content, Name: m.OfDeveloper.Name.Or("")}, nil
	case m.OfSystem != nil:
		content, err := readTextFromSystem(m.OfSystem.Content)
		if err != nil {
			return chat.Message{}, err
		}
		return chat.Message{Role: chat.RoleSystem, Content: content, Name: m.OfSystem.Name.Or("")}, nil
	case m.OfUser != nil:
		content, err := readTextFromUser(m.OfUser.Content)
		if err != nil {
			return chat.Message{}, err
		}
		return chat.Message{Role: chat.RoleUser, Content: content, Name: m.OfUser.Name.Or("")}, nil
	case m.OfAssistant != nil:
		content, err := readTextFromAssistant(m.OfAssistant.Content)
		if err != nil {
			return chat.Message{}, err
		}
		msg := chat.Message{Role: chat.RoleAssistant, Content: content, Name: m.OfAssistant.Name.Or("")}
		if len(m.OfAssistant.ToolCalls) > 0 {
			msg.ToolCalls = toToolCalls(m.OfAssistant.ToolCalls)
		}
		return msg, nil
	case m.OfTool != nil:
		content := readTextFromTool(m.OfTool.Content)
		return chat.Message{Role: chat.RoleTool, Content: content, ToolCallID: m.OfTool.ToolCallID}, nil
	case m.OfFunction != nil:
		if m.OfFunction.Name == "" {
			return chat.Message{}, fmt.Errorf("function message name is required")
		}
		return chat.Message{Role: chat.RoleAssistant, Content: m.OfFunction.Content.Or(""), Name: m.OfFunction.Name}, nil
	default:
		return chat.Message{}, fmt.Errorf("unsupported message type")
	}
}

func readTextFromSystem(content openai.ChatCompletionSystemMessageParamContentUnion) (string, error) {
	if content.OfString.Valid() {
		return content.OfString.Value, nil
	}
	if len(content.OfArrayOfContentParts) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(content.OfArrayOfContentParts))
	for _, part := range content.OfArrayOfContentParts {
		text := strings.TrimSpace(part.Text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("unsupported system content parts")
	}
	return strings.Join(parts, "\n"), nil
}

func readTextFromDeveloper(content openai.ChatCompletionDeveloperMessageParamContentUnion) (string, error) {
	if content.OfString.Valid() {
		return content.OfString.Value, nil
	}
	if len(content.OfArrayOfContentParts) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(content.OfArrayOfContentParts))
	for _, part := range content.OfArrayOfContentParts {
		text := strings.TrimSpace(part.Text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("unsupported developer content parts")
	}
	return strings.Join(parts, "\n"), nil
}

func readTextFromUser(content openai.ChatCompletionUserMessageParamContentUnion) (string, error) {
	if content.OfString.Valid() {
		return content.OfString.Value, nil
	}
	if len(content.OfArrayOfContentParts) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(content.OfArrayOfContentParts))
	for _, part := range content.OfArrayOfContentParts {
		if part.OfText != nil {
			text := strings.TrimSpace(part.OfText.Text)
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("unsupported user content parts")
	}
	return strings.Join(parts, "\n"), nil
}

func readTextFromAssistant(content openai.ChatCompletionAssistantMessageParamContentUnion) (string, error) {
	if content.OfString.Valid() {
		return content.OfString.Value, nil
	}
	if len(content.OfArrayOfContentParts) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(content.OfArrayOfContentParts))
	for _, part := range content.OfArrayOfContentParts {
		if part.OfText != nil {
			text := strings.TrimSpace(part.OfText.Text)
			if text != "" {
				parts = append(parts, text)
			}
		} else if part.OfRefusal != nil {
			text := strings.TrimSpace(part.OfRefusal.Refusal)
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("unsupported assistant content parts")
	}
	return strings.Join(parts, "\n"), nil
}

func readTextFromTool(content openai.ChatCompletionToolMessageParamContentUnion) string {
	if content.OfString.Valid() {
		return content.OfString.Value
	}
	if len(content.OfArrayOfContentParts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(content.OfArrayOfContentParts))
	for _, part := range content.OfArrayOfContentParts {
		text := strings.TrimSpace(part.Text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func toTools(in []openai.ChatCompletionToolUnionParam) ([]chat.Tool, error) {
	tools := make([]chat.Tool, 0, len(in))
	for _, t := range in {
		fn := t.GetFunction()
		if fn == nil {
			continue
		}
		tool := chat.Tool{
			Type: "function",
			Function: chat.ToolFunction{
				Name: fn.Name,
			},
		}
		if fn.Description.Valid() {
			tool.Function.Description = fn.Description.Value
		}
		if fn.Strict.Valid() {
			v := fn.Strict.Value
			tool.Function.Strict = &v
		}
		if len(fn.Parameters) > 0 {
			data, err := json.Marshal(fn.Parameters)
			if err != nil {
				return nil, err
			}
			tool.Function.ParametersJSONSchema = data
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

func toToolChoice(choice openai.ChatCompletionToolChoiceOptionUnionParam) (chat.ToolChoice, bool) {
	if choice.OfAuto.Valid() {
		switch choice.OfAuto.Value {
		case string(openai.ChatCompletionToolChoiceOptionAutoAuto):
			return chat.ToolChoiceAuto(), true
		case string(openai.ChatCompletionToolChoiceOptionAutoNone):
			return chat.ToolChoiceNone(), true
		case string(openai.ChatCompletionToolChoiceOptionAutoRequired):
			return chat.ToolChoiceRequired(), true
		}
	}
	if choice.OfFunctionToolChoice != nil {
		name := choice.OfFunctionToolChoice.Function.Name
		if name != "" {
			return chat.ToolChoiceFunction(name), true
		}
	}
	return chat.ToolChoice{}, false
}

func toToolCalls(calls []openai.ChatCompletionMessageToolCallUnionParam) []chat.ToolCall {
	out := make([]chat.ToolCall, 0, len(calls))
	for _, call := range calls {
		if call.OfFunction == nil {
			continue
		}
		out = append(out, chat.ToolCall{
			ID:   call.OfFunction.ID,
			Type: "function",
			Function: chat.ToolCallFunction{
				Name:      call.OfFunction.Function.Name,
				Arguments: call.OfFunction.Function.Arguments,
			},
		})
	}
	return out
}

func toOpenAIResponse(result *chat.Result, model string) openai.ChatCompletion {
	msg := openai.ChatCompletionMessage{
		Role:    constant.ValueOf[constant.Assistant](),
		Content: result.Text,
	}
	if len(result.ToolCalls) > 0 {
		msg.ToolCalls = make([]openai.ChatCompletionMessageToolCallUnion, 0, len(result.ToolCalls))
		for _, tc := range result.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, openai.ChatCompletionMessageToolCallUnion{
				ID:   tc.ID,
				Type: "function",
				Function: openai.ChatCompletionMessageFunctionToolCallFunction{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	}

	resp := openai.ChatCompletion{
		ID:      "",
		Object:  constant.ValueOf[constant.ChatCompletion](),
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []openai.ChatCompletionChoice{{
			Index:        0,
			Message:      msg,
			FinishReason: finishReason(result),
		}},
		Usage: openai.CompletionUsage{
			PromptTokens:     int64(result.Usage.InputTokens),
			CompletionTokens: int64(result.Usage.OutputTokens),
			TotalTokens:      int64(result.Usage.TotalTokens),
		},
	}
	if result.Model != "" {
		resp.Model = result.Model
	}
	return resp
}

func finishReason(result *chat.Result) string {
	if result != nil && len(result.ToolCalls) > 0 {
		return "tool_calls"
	}
	return "stop"
}

func toOpenAIOptions(req openai.ChatCompletionNewParams) structs.JSONMap {
	opts := structs.NewJSONMap()
	if req.N.Valid() && req.N.Value > 0 {
		opts["n"] = req.N.Value
	}
	if req.Seed.Valid() {
		opts["seed"] = req.Seed.Value
	}
	if req.Logprobs.Valid() {
		opts["logprobs"] = req.Logprobs.Value
	}
	if req.TopLogprobs.Valid() {
		opts["top_logprobs"] = req.TopLogprobs.Value
	}
	if req.ParallelToolCalls.Valid() {
		opts["parallel_tool_calls"] = req.ParallelToolCalls.Value
	}
	if req.Store.Valid() {
		opts["store"] = req.Store.Value
	}
	if req.PromptCacheKey.Valid() {
		opts["prompt_cache_key"] = req.PromptCacheKey.Value
	}
	if req.SafetyIdentifier.Valid() {
		opts["safety_identifier"] = req.SafetyIdentifier.Value
	}
	if req.ReasoningEffort != "" {
		opts["reasoning_effort"] = string(req.ReasoningEffort)
	}
	if req.Verbosity != "" {
		opts["verbosity"] = string(req.Verbosity)
	}
	if req.ServiceTier != "" {
		opts["service_tier"] = string(req.ServiceTier)
	}
	if len(req.Modalities) > 0 {
		opts["modalities"] = append([]string{}, req.Modalities...)
	}
	if len(req.LogitBias) > 0 {
		bias := make(map[string]any, len(req.LogitBias))
		for k, v := range req.LogitBias {
			bias[k] = v
		}
		opts["logit_bias"] = bias
	}
	if len(req.Metadata) > 0 {
		meta := make(map[string]any, len(req.Metadata))
		for k, v := range req.Metadata {
			meta[k] = v
		}
		opts["metadata"] = meta
	}
	if rf := toResponseFormatOption(req.ResponseFormat); rf != nil {
		opts["response_format"] = rf
	}
	if len(opts) == 0 {
		return nil
	}
	return opts
}

func toResponseFormatOption(format openai.ChatCompletionNewParamsResponseFormatUnion) any {
	if format.OfText != nil {
		return "text"
	}
	if format.OfJSONObject != nil {
		return "json_object"
	}
	if format.OfJSONSchema == nil {
		return nil
	}
	schema := format.OfJSONSchema.JSONSchema
	if schema.Name == "" {
		return nil
	}
	jsonSchema := map[string]any{
		"name": schema.Name,
	}
	if schema.Strict.Valid() {
		jsonSchema["strict"] = schema.Strict.Value
	}
	if schema.Description.Valid() {
		jsonSchema["description"] = schema.Description.Value
	}
	if schema.Schema != nil {
		jsonSchema["schema"] = schema.Schema
	}
	return map[string]any{
		"type":        "json_schema",
		"json_schema": jsonSchema,
	}
}
