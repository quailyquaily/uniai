// Package oaicompat provides shared conversion helpers for providers
// that use the OpenAI-compatible API (openai, azure).
package oaicompat

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/lyricat/goutils/structs"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/toolschema"
)

const geminiThoughtSignatureValidatorBypass = "skip_thought_signature_validator"

// ToMessages converts chat.Message slice to OpenAI SDK message params.
func ToMessages(input []chat.Message, model string) ([]openai.ChatCompletionMessageParamUnion, error) {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(input))
	for _, m := range input {
		switch m.Role {
		case chat.RoleSystem:
			text, err := chat.MessageText(m)
			if err != nil {
				return nil, fmt.Errorf("role %q: %w", m.Role, err)
			}
			msg := openai.ChatCompletionSystemMessageParam{
				Content: openai.ChatCompletionSystemMessageParamContentUnion{OfString: openai.String(text)},
			}
			if m.Name != "" {
				msg.Name = openai.String(m.Name)
			}
			out = append(out, openai.ChatCompletionMessageParamUnion{OfSystem: &msg})
		case chat.RoleUser:
			content, err := toUserContent(m)
			if err != nil {
				return nil, fmt.Errorf("role %q: %w", m.Role, err)
			}
			msg := openai.ChatCompletionUserMessageParam{Content: content}
			if m.Name != "" {
				msg.Name = openai.String(m.Name)
			}
			out = append(out, openai.ChatCompletionMessageParamUnion{OfUser: &msg})
		case chat.RoleAssistant:
			text, err := chat.MessageText(m)
			if err != nil {
				return nil, fmt.Errorf("role %q: %w", m.Role, err)
			}
			msg := openai.ChatCompletionAssistantMessageParam{}
			if text != "" {
				msg.Content = openai.ChatCompletionAssistantMessageParamContentUnion{OfString: openai.String(text)}
			}
			if m.Name != "" {
				msg.Name = openai.String(m.Name)
			}
			if len(m.ToolCalls) > 0 {
				msg.ToolCalls = ToToolCallParams(m.ToolCalls, model)
			}
			out = append(out, openai.ChatCompletionMessageParamUnion{OfAssistant: &msg})
		case chat.RoleTool:
			if m.ToolCallID == "" {
				return nil, fmt.Errorf("tool_call_id is required for tool messages")
			}
			text, err := chat.MessageText(m)
			if err != nil {
				return nil, fmt.Errorf("role %q: %w", m.Role, err)
			}
			out = append(out, openai.ToolMessage(text, m.ToolCallID))
		default:
			text, err := chat.MessageText(m)
			if err != nil {
				return nil, fmt.Errorf("role %q: %w", m.Role, err)
			}
			out = append(out, openai.UserMessage(text))
		}
	}
	return out, nil
}

func toUserContent(m chat.Message) (openai.ChatCompletionUserMessageParamContentUnion, error) {
	parts := chat.NormalizeMessageParts(m)
	if len(parts) == 0 {
		return openai.ChatCompletionUserMessageParamContentUnion{OfString: openai.String("")}, nil
	}
	if len(parts) == 1 && parts[0].Type == chat.PartTypeText {
		return openai.ChatCompletionUserMessageParamContentUnion{OfString: openai.String(parts[0].Text)}, nil
	}
	out := make([]openai.ChatCompletionContentPartUnionParam, 0, len(parts))
	for i, part := range parts {
		item, err := toUserPart(part)
		if err != nil {
			return openai.ChatCompletionUserMessageParamContentUnion{}, fmt.Errorf("part[%d]: %w", i, err)
		}
		out = append(out, item)
	}
	return openai.ChatCompletionUserMessageParamContentUnion{OfArrayOfContentParts: out}, nil
}

func toUserPart(part chat.Part) (openai.ChatCompletionContentPartUnionParam, error) {
	if err := chat.ValidatePart(part); err != nil {
		return openai.ChatCompletionContentPartUnionParam{}, err
	}
	switch part.Type {
	case chat.PartTypeText:
		return openai.TextContentPart(part.Text), nil
	case chat.PartTypeImageURL:
		return openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
			URL: strings.TrimSpace(part.URL),
		}), nil
	case chat.PartTypeImageBase64:
		mimeType := strings.TrimSpace(part.MIMEType)
		if mimeType == "" {
			mimeType = "image/png"
		}
		dataURL := fmt.Sprintf("data:%s;base64,%s", mimeType, strings.TrimSpace(part.DataBase64))
		return openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
			URL: dataURL,
		}), nil
	default:
		return openai.ChatCompletionContentPartUnionParam{}, fmt.Errorf("unsupported part type %q", part.Type)
	}
}

// ToToolParams converts chat.Tool slice to OpenAI SDK tool params.
func ToToolParams(tools []chat.Tool) ([]openai.ChatCompletionToolUnionParam, error) {
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
			toolschema.Normalize(params)
			fn.Parameters = shared.FunctionParameters(params)
		}
		out = append(out, openai.ChatCompletionFunctionTool(fn))
	}
	return out, nil
}

// ToToolChoice converts chat.ToolChoice to OpenAI SDK tool choice param.
func ToToolChoice(choice *chat.ToolChoice) openai.ChatCompletionToolChoiceOptionUnionParam {
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

// ToToolCallParams converts chat.ToolCall slice to OpenAI SDK tool call params.
func ToToolCallParams(calls []chat.ToolCall, model string) []openai.ChatCompletionMessageToolCallUnionParam {
	out := make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(calls))
	attachGeminiThoughtSignature := isGeminiOpenAIModel(model)
	for _, call := range calls {
		if call.Type != "" && call.Type != "function" {
			continue
		}
		if call.ID == "" || call.Function.Name == "" {
			continue
		}
		toolCall := openai.ChatCompletionMessageFunctionToolCallParam{
			ID: call.ID,
			Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		}
		if attachGeminiThoughtSignature {
			signature := resolveGeminiThoughtSignature(call)
			toolCall.SetExtraFields(map[string]any{
				"extra_content": map[string]any{
					"google": map[string]any{
						"thought_signature": signature,
					},
				},
			})
		}
		out = append(out, openai.ChatCompletionMessageToolCallUnionParam{
			OfFunction: &toolCall,
		})
	}
	return out
}

// ToToolCalls converts OpenAI SDK tool call unions to chat.ToolCall slice.
func ToToolCalls(calls []openai.ChatCompletionMessageToolCallUnion) []chat.ToolCall {
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

func isGeminiOpenAIModel(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	normalized = strings.TrimPrefix(normalized, "models/")
	return strings.HasPrefix(normalized, "gemini-") || strings.Contains(normalized, "/gemini-")
}

func resolveGeminiThoughtSignature(call chat.ToolCall) string {
	sig := strings.TrimSpace(call.ThoughtSignature)
	if sig != "" {
		return sig
	}
	return geminiThoughtSignatureValidatorBypass
}

// ApplyOptions applies shared OpenAI-compatible option fields to params.
func ApplyOptions(params *openai.ChatCompletionNewParams, opts structs.JSONMap) {
	if params == nil || len(opts) == 0 {
		return
	}
	opt := &opts
	if opt.HasKey("n") {
		if n := opt.GetInt64("n"); n > 0 {
			params.N = openai.Int(n)
		}
	}
	if opt.HasKey("seed") {
		params.Seed = openai.Int(opt.GetInt64("seed"))
	}
	if opt.HasKey("logprobs") {
		params.Logprobs = openai.Bool(opt.GetBool("logprobs"))
	}
	if opt.HasKey("top_logprobs") {
		if top := opt.GetInt64("top_logprobs"); top > 0 {
			params.TopLogprobs = openai.Int(top)
		}
	}
	if opt.HasKey("parallel_tool_calls") {
		params.ParallelToolCalls = openai.Bool(opt.GetBool("parallel_tool_calls"))
	}
	if opt.HasKey("store") {
		params.Store = openai.Bool(opt.GetBool("store"))
	}
	if opt.HasKey("prompt_cache_key") {
		if val := strings.TrimSpace(opt.GetString("prompt_cache_key")); val != "" {
			params.PromptCacheKey = openai.String(val)
		}
	}
	if opt.HasKey("safety_identifier") {
		if val := strings.TrimSpace(opt.GetString("safety_identifier")); val != "" {
			params.SafetyIdentifier = openai.String(val)
		}
	}
	if opt.HasKey("reasoning_effort") {
		if val := strings.TrimSpace(opt.GetString("reasoning_effort")); val != "" {
			params.ReasoningEffort = shared.ReasoningEffort(val)
		}
	}
	if opt.HasKey("verbosity") {
		if val := strings.TrimSpace(opt.GetString("verbosity")); val != "" {
			params.Verbosity = openai.ChatCompletionNewParamsVerbosity(val)
		}
	}
	if opt.HasKey("service_tier") {
		if val := strings.TrimSpace(opt.GetString("service_tier")); val != "" {
			params.ServiceTier = openai.ChatCompletionNewParamsServiceTier(val)
		}
	}
	if opt.HasKey("modalities") {
		if modalities := opt.GetStringArray("modalities"); len(modalities) > 0 {
			params.Modalities = append([]string{}, modalities...)
		}
	}
	if opt.HasKey("logit_bias") {
		if bias := ParseLogitBias((*opt)["logit_bias"]); len(bias) > 0 {
			params.LogitBias = bias
		}
	}
	if opt.HasKey("metadata") {
		if meta := ParseStringMap((*opt)["metadata"]); len(meta) > 0 {
			params.Metadata = shared.Metadata(meta)
		}
	}
	if opt.HasKey("response_format") {
		ApplyResponseFormat(params, (*opt)["response_format"])
	}
}

// ApplyResponseFormat sets the response format on params from a raw option value.
func ApplyResponseFormat(params *openai.ChatCompletionNewParams, value any) {
	switch v := value.(type) {
	case string:
		SetResponseFormatByType(params, v, nil)
	case map[string]any:
		SetResponseFormatByType(params, "", v)
	case structs.JSONMap:
		SetResponseFormatByType(params, "", map[string]any(v))
	}
}

// SetResponseFormatByType sets the response format on params from a type name and optional payload.
func SetResponseFormatByType(params *openai.ChatCompletionNewParams, typeName string, payload map[string]any) {
	if params == nil {
		return
	}
	typ := strings.ToLower(strings.TrimSpace(typeName))
	if typ == "" && payload != nil {
		if raw, ok := payload["type"]; ok {
			if s, ok := raw.(string); ok {
				typ = strings.ToLower(strings.TrimSpace(s))
			}
		}
	}
	switch typ {
	case "text":
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfText: &shared.ResponseFormatTextParam{Type: "text"},
		}
	case "json_object":
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{Type: "json_object"},
		}
	case "json_schema":
		schemaPayload := payload
		if payload != nil {
			if raw, ok := payload["json_schema"]; ok {
				switch s := raw.(type) {
				case map[string]any:
					schemaPayload = s
				case structs.JSONMap:
					schemaPayload = map[string]any(s)
				}
			}
		}
		if schemaPayload == nil {
			return
		}
		name, _ := schemaPayload["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		jsonSchema := shared.ResponseFormatJSONSchemaJSONSchemaParam{
			Name: name,
		}
		if raw, ok := schemaPayload["strict"]; ok {
			if strict, ok := raw.(bool); ok {
				jsonSchema.Strict = openai.Bool(strict)
			}
		}
		if raw, ok := schemaPayload["description"]; ok {
			if desc, ok := raw.(string); ok && strings.TrimSpace(desc) != "" {
				jsonSchema.Description = openai.String(desc)
			}
		}
		if raw, ok := schemaPayload["schema"]; ok {
			jsonSchema.Schema = raw
		}
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{JSONSchema: jsonSchema},
		}
	}
}

// ParseLogitBias extracts a map[string]int64 from a raw option value.
func ParseLogitBias(value any) map[string]int64 {
	out := map[string]int64{}
	switch m := value.(type) {
	case map[string]any:
		for k, v := range m {
			if val, ok := ToInt64(v); ok {
				out[k] = val
			}
		}
	case structs.JSONMap:
		for k, v := range m {
			if val, ok := ToInt64(v); ok {
				out[k] = val
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ParseStringMap extracts a map[string]string from a raw option value.
func ParseStringMap(value any) map[string]string {
	out := map[string]string{}
	switch m := value.(type) {
	case map[string]any:
		for k, v := range m {
			out[k] = fmt.Sprint(v)
		}
	case structs.JSONMap:
		for k, v := range m {
			out[k] = fmt.Sprint(v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ToInt64 converts various numeric types to int64.
func ToInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case json.Number:
		if val, err := v.Int64(); err == nil {
			return val, true
		}
	case string:
		if val, err := strconv.ParseInt(v, 10, 64); err == nil {
			return val, true
		}
	}
	return 0, false
}
