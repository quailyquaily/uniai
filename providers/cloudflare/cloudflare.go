package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/diag"
	cf "github.com/quailyquaily/uniai/internal/providers/cloudflare"
	"github.com/quailyquaily/uniai/internal/toolschema"
)

type Config struct {
	AccountID string
	APIToken  string
	APIBase   string
	Debug     bool
}

type Provider struct {
	cfg Config
}

func New(cfg Config) (*Provider, error) {
	if cfg.AccountID == "" {
		return nil, fmt.Errorf("cloudflare account id is required")
	}
	if cfg.APIToken == "" {
		return nil, fmt.Errorf("cloudflare api token is required")
	}
	return &Provider{cfg: cfg}, nil
}

func (p *Provider) Chat(ctx context.Context, req *chat.Request) (*chat.Result, error) {
	debugFn := req.Options.DebugFn
	model := req.Model
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}
	if req.Options.OnStream != nil {
		return nil, fmt.Errorf("cloudflare provider does not support streaming yet")
	}

	payload, err := buildPayload(req, model)
	if err != nil {
		return nil, err
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	diag.LogText(p.cfg.Debug, debugFn, "cloudflare.chat.request", string(reqBody))

	resultRaw, err := cf.RunJSON(ctx, p.cfg.APIToken, p.cfg.APIBase, p.cfg.AccountID, model, payload)
	if err != nil {
		return nil, err
	}
	diag.LogText(p.cfg.Debug, debugFn, "cloudflare.chat.response", string(resultRaw))

	return toChatResult(resultRaw, model), nil
}

func buildPayload(req *chat.Request, model string) (structs.JSONMap, error) {
	payload := structs.NewJSONMap()
	payload.Merge(req.Options.Cloudflare)
	if payload.GetBool("stream") {
		return nil, fmt.Errorf("cloudflare streaming is not supported; remove stream option")
	}

	responsesCompatible := isGptOssModel(model)
	if !hasAnyKey(&payload, "messages", "prompt", "input") {
		if responsesCompatible {
			input, err := toResponsesInput(req.Messages)
			if err != nil {
				return nil, err
			}
			payload["input"] = input
		} else {
			messages, err := toScopedMessages(req.Messages)
			if err != nil {
				return nil, err
			}
			payload["messages"] = messages
		}
	}
	if len(req.Tools) > 0 && !payload.HasKey("tools") {
		tools, err := toCloudflareTools(req.Tools, responsesCompatible)
		if err != nil {
			return nil, err
		}
		if len(tools) > 0 {
			payload["tools"] = tools
		}
	}
	if req.ToolChoice != nil && !payload.HasKey("tool_choice") {
		choice, err := toCloudflareToolChoice(req.ToolChoice, responsesCompatible)
		if err != nil {
			return nil, err
		}
		if choice != nil {
			payload["tool_choice"] = choice
		}
	}

	applyCommonOptions(&payload, req.Options)
	return payload, nil
}

func applyCommonOptions(payload *structs.JSONMap, opts chat.Options) {
	if opts.Temperature != nil && !payload.HasKey("temperature") {
		payload.SetValue("temperature", *opts.Temperature)
	}
	if opts.TopP != nil && !payload.HasKey("top_p") {
		payload.SetValue("top_p", *opts.TopP)
	}
	if opts.MaxTokens != nil && !payload.HasKey("max_tokens") {
		payload.SetValue("max_tokens", *opts.MaxTokens)
	}
	if len(opts.Stop) > 0 && !payload.HasKey("stop") {
		payload.SetValue("stop", append([]string{}, opts.Stop...))
	}
	if opts.PresencePenalty != nil && !payload.HasKey("presence_penalty") {
		payload.SetValue("presence_penalty", *opts.PresencePenalty)
	}
	if opts.FrequencyPenalty != nil && !payload.HasKey("frequency_penalty") {
		payload.SetValue("frequency_penalty", *opts.FrequencyPenalty)
	}
}

func toScopedMessages(msgs []chat.Message) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case chat.RoleSystem, chat.RoleUser:
			if strings.TrimSpace(m.Content) == "" {
				continue
			}
			msg := map[string]any{
				"role":    m.Role,
				"content": m.Content,
			}
			if name := strings.TrimSpace(m.Name); name != "" {
				msg["name"] = name
			}
			out = append(out, msg)
		case chat.RoleAssistant:
			msg := map[string]any{
				"role": chat.RoleAssistant,
			}
			if strings.TrimSpace(m.Content) != "" {
				msg["content"] = m.Content
			}
			if name := strings.TrimSpace(m.Name); name != "" {
				msg["name"] = name
			}
			if len(m.ToolCalls) > 0 {
				calls, err := toChatCompletionToolCalls(m.ToolCalls)
				if err != nil {
					return nil, err
				}
				if len(calls) > 0 {
					msg["tool_calls"] = calls
				}
			}
			if len(msg) == 1 {
				continue
			}
			out = append(out, msg)
		case chat.RoleTool:
			if strings.TrimSpace(m.ToolCallID) == "" {
				return nil, fmt.Errorf("tool_call_id is required for tool messages")
			}
			msg := map[string]any{
				"role":         chat.RoleTool,
				"tool_call_id": m.ToolCallID,
				"content":      m.Content,
			}
			out = append(out, msg)
		default:
			if strings.TrimSpace(m.Content) == "" {
				continue
			}
			out = append(out, map[string]any{
				"role":    m.Role,
				"content": m.Content,
			})
		}
	}
	return out, nil
}

func toResponsesInput(msgs []chat.Message) ([]any, error) {
	out := make([]any, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case chat.RoleSystem, chat.RoleUser:
			if strings.TrimSpace(m.Content) == "" {
				continue
			}
			msg := map[string]any{
				"role":    m.Role,
				"content": m.Content,
			}
			if name := strings.TrimSpace(m.Name); name != "" {
				msg["name"] = name
			}
			out = append(out, msg)
		case chat.RoleAssistant:
			if strings.TrimSpace(m.Content) != "" {
				msg := map[string]any{
					"role":    chat.RoleAssistant,
					"content": m.Content,
				}
				if name := strings.TrimSpace(m.Name); name != "" {
					msg["name"] = name
				}
				out = append(out, msg)
			}
			if len(m.ToolCalls) > 0 {
				calls, err := toResponsesFunctionCalls(m.ToolCalls)
				if err != nil {
					return nil, err
				}
				for _, call := range calls {
					out = append(out, call)
				}
			}
		case chat.RoleTool:
			if strings.TrimSpace(m.ToolCallID) == "" {
				return nil, fmt.Errorf("tool_call_id is required for tool messages")
			}
			out = append(out, map[string]any{
				"type":    "function_call_output",
				"call_id": m.ToolCallID,
				"output":  m.Content,
			})
		default:
			if strings.TrimSpace(m.Content) == "" {
				continue
			}
			out = append(out, map[string]any{
				"role":    m.Role,
				"content": m.Content,
			})
		}
	}
	return out, nil
}

func toChatCompletionToolCalls(calls []chat.ToolCall) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(calls))
	for i, call := range calls {
		item, err := toChatCompletionToolCall(call, i)
		if err != nil {
			return nil, err
		}
		if item == nil {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func toChatCompletionToolCall(call chat.ToolCall, idx int) (map[string]any, error) {
	name := strings.TrimSpace(call.Function.Name)
	if name == "" {
		return nil, fmt.Errorf("assistant tool call name is required")
	}
	callType, ok := normalizeToolCallType(call.Type)
	if !ok {
		return nil, fmt.Errorf("unsupported assistant tool call type %q", call.Type)
	}
	callID := strings.TrimSpace(call.ID)
	if callID == "" {
		callID = fmt.Sprintf("call_%d", idx+1)
	}
	return map[string]any{
		"id":   callID,
		"type": callType,
		"function": map[string]any{
			"name":      name,
			"arguments": normalizeToolCallArguments(call.Function.Arguments),
		},
	}, nil
}

func toResponsesFunctionCalls(calls []chat.ToolCall) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(calls))
	for i, call := range calls {
		item, err := toResponsesFunctionCall(call, i)
		if err != nil {
			return nil, err
		}
		if item == nil {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func toResponsesFunctionCall(call chat.ToolCall, idx int) (map[string]any, error) {
	name := strings.TrimSpace(call.Function.Name)
	if name == "" {
		return nil, fmt.Errorf("assistant tool call name is required")
	}
	callType, ok := normalizeToolCallType(call.Type)
	if !ok {
		return nil, fmt.Errorf("unsupported assistant tool call type %q", call.Type)
	}
	if callType != "function" {
		return nil, fmt.Errorf("unsupported responses tool call type %q", callType)
	}
	callID := strings.TrimSpace(call.ID)
	if callID == "" {
		callID = fmt.Sprintf("call_%d", idx+1)
	}
	return map[string]any{
		"type":      "function_call",
		"name":      name,
		"arguments": normalizeToolCallArguments(call.Function.Arguments),
		"call_id":   callID,
	}, nil
}

func normalizeToolCallArguments(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}"
	}
	return raw
}

func toCloudflareTools(tools []chat.Tool, responsesCompatible bool) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "function" {
			continue
		}
		name := strings.TrimSpace(tool.Function.Name)
		if name == "" {
			continue
		}
		params := map[string]any{"type": "object"}
		if len(tool.Function.ParametersJSONSchema) > 0 {
			if err := json.Unmarshal(tool.Function.ParametersJSONSchema, &params); err != nil {
				return nil, err
			}
			toolschema.Normalize(params)
		}
		if responsesCompatible {
			item := map[string]any{
				"type":       "function",
				"name":       name,
				"parameters": params,
			}
			if desc := strings.TrimSpace(tool.Function.Description); desc != "" {
				item["description"] = desc
			}
			if tool.Function.Strict != nil {
				item["strict"] = *tool.Function.Strict
			}
			out = append(out, item)
			continue
		}
		item := map[string]any{
			"name":       name,
			"parameters": params,
		}
		if desc := strings.TrimSpace(tool.Function.Description); desc != "" {
			item["description"] = desc
		}
		out = append(out, item)
	}
	return out, nil
}

func toCloudflareToolChoice(choice *chat.ToolChoice, responsesCompatible bool) (any, error) {
	if choice == nil {
		return nil, nil
	}
	switch strings.TrimSpace(choice.Mode) {
	case "", "auto":
		return "auto", nil
	case "none":
		return "none", nil
	case "required":
		return "required", nil
	case "function":
		name := strings.TrimSpace(choice.FunctionName)
		if name == "" {
			return nil, fmt.Errorf("tool_choice function_name is required when mode=function")
		}
		if !responsesCompatible {
			return map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": name,
				},
			}, nil
		}
		return map[string]any{
			"type": "function",
			"name": name,
		}, nil
	default:
		return nil, nil
	}
}

func toChatResult(resultRaw []byte, fallbackModel string) *chat.Result {
	var raw any
	if err := json.Unmarshal(resultRaw, &raw); err != nil {
		return &chat.Result{Warnings: []string{fmt.Sprintf("cloudflare response parse failed: %v", err)}}
	}
	result := &chat.Result{Raw: raw}
	if m, ok := raw.(map[string]any); ok {
		result.Text = extractText(m)
		result.Model = extractString(m, "model")
		result.ToolCalls = extractToolCalls(m)
		if usage := extractUsage(m); usage != nil {
			result.Usage = *usage
		}
	}
	if result.Model == "" {
		result.Model = fallbackModel
	}
	return result
}

func extractText(m map[string]any) string {
	if text := extractString(m, "response"); text != "" {
		return text
	}
	if text := extractString(m, "text"); text != "" {
		return text
	}
	if text := extractString(m, "output_text"); text != "" {
		return text
	}
	if output, ok := m["output"].([]any); ok {
		if text := extractOutputText(output); text != "" {
			return text
		}
	}
	if choices, ok := m["choices"].([]any); ok {
		if text := extractChoicesText(choices); text != "" {
			return text
		}
	}
	return ""
}

func extractOutputText(output []any) string {
	for _, item := range output {
		msg, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text := extractString(msg, "text"); text != "" {
			return text
		}
		if content, ok := msg["content"].([]any); ok {
			parts := make([]string, 0, len(content))
			for _, part := range content {
				partMap, ok := part.(map[string]any)
				if !ok {
					continue
				}
				if text := extractString(partMap, "text"); text != "" {
					parts = append(parts, text)
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, "")
			}
		}
	}
	return ""
}

func extractChoicesText(choices []any) string {
	for _, choice := range choices {
		choiceMap, ok := choice.(map[string]any)
		if !ok {
			continue
		}
		if text := extractString(choiceMap, "text"); text != "" {
			return text
		}
		if msg, ok := choiceMap["message"].(map[string]any); ok {
			if text := extractString(msg, "content"); text != "" {
				return text
			}
		}
	}
	return ""
}

func extractToolCalls(m map[string]any) []chat.ToolCall {
	if raw := m["tool_calls"]; raw != nil {
		if calls := parseToolCalls(raw); len(calls) > 0 {
			return calls
		}
	}
	if output, ok := m["output"].([]any); ok {
		if calls := parseToolCalls(output); len(calls) > 0 {
			return calls
		}
	}
	if choices, ok := m["choices"].([]any); ok {
		for _, choice := range choices {
			choiceMap, ok := choice.(map[string]any)
			if !ok {
				continue
			}
			if msg, ok := choiceMap["message"].(map[string]any); ok {
				if calls := parseToolCalls(msg["tool_calls"]); len(calls) > 0 {
					return calls
				}
			}
		}
	}
	return nil
}

func parseToolCalls(raw any) []chat.ToolCall {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]chat.ToolCall, 0, len(arr))
	for _, item := range arr {
		call, ok := item.(map[string]any)
		if !ok {
			continue
		}
		parsed, ok := parseToolCall(call)
		if !ok {
			continue
		}
		out = append(out, parsed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseToolCall(call map[string]any) (chat.ToolCall, bool) {
	fn, _ := call["function"].(map[string]any)
	name := strings.TrimSpace(extractString(fn, "name"))
	if name == "" {
		name = strings.TrimSpace(extractString(call, "name"))
	}
	if name == "" {
		return chat.ToolCall{}, false
	}
	callType, ok := normalizeToolCallType(extractString(call, "type"))
	if !ok {
		return chat.ToolCall{}, false
	}
	var rawArgs any
	hasArgs := false
	if fn != nil {
		if raw, ok := fn["arguments"]; ok {
			rawArgs = raw
			hasArgs = true
		}
	}
	if !hasArgs {
		if raw, ok := call["arguments"]; ok {
			rawArgs = raw
			hasArgs = true
		} else if raw, ok := call["input"]; ok {
			rawArgs = raw
			hasArgs = true
		}
	}
	arguments := toArgumentsString(rawArgs)
	return chat.ToolCall{
		ID:   firstNonEmptyString(extractString(call, "id"), extractString(call, "call_id")),
		Type: callType,
		Function: chat.ToolCallFunction{
			Name:      name,
			Arguments: arguments,
		},
	}, true
}

func normalizeToolCallType(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "function", "function_call", "tool_call":
		return "function", true
	default:
		return "", false
	}
}

func toArgumentsString(raw any) string {
	switch v := raw.(type) {
	case nil:
		return "{}"
	case string:
		if strings.TrimSpace(v) == "" {
			return "{}"
		}
		return v
	case []byte:
		if len(v) == 0 {
			return "{}"
		}
		return string(v)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return "{}"
		}
		return string(data)
	}
}

func firstNonEmptyString(vals ...string) string {
	for _, val := range vals {
		if strings.TrimSpace(val) != "" {
			return val
		}
	}
	return ""
}

func extractUsage(m map[string]any) *chat.Usage {
	u, ok := m["usage"].(map[string]any)
	if !ok {
		return nil
	}
	usage := &chat.Usage{}
	usage.InputTokens = int(extractNumber(u, "prompt_tokens", "input_tokens"))
	usage.OutputTokens = int(extractNumber(u, "completion_tokens", "output_tokens"))
	usage.TotalTokens = int(extractNumber(u, "total_tokens"))
	return usage
}

func extractNumber(m map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if val, ok := m[key]; ok {
			switch v := val.(type) {
			case float64:
				return v
			case float32:
				return float64(v)
			case int:
				return float64(v)
			case int64:
				return float64(v)
			}
		}
	}
	return 0
}

func extractString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if val, ok := m[key]; ok {
		switch v := val.(type) {
		case string:
			return v
		case fmt.Stringer:
			return v.String()
		}
	}
	return ""
}

func hasAnyKey(m *structs.JSONMap, keys ...string) bool {
	for _, key := range keys {
		if m.HasKey(key) {
			return true
		}
	}
	return false
}

func isGptOssModel(model string) bool {
	model = strings.ToLower(model)
	return strings.Contains(model, "gpt-oss")
}
