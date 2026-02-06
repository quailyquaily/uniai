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

	payload := structs.NewJSONMap()
	payload.Merge(req.Options.Cloudflare)
	if payload.GetBool("stream") {
		return nil, fmt.Errorf("cloudflare streaming is not supported; remove stream option")
	}

	if !hasAnyKey(&payload, "messages", "prompt", "input") {
		if isGptOssModel(model) {
			payload["input"] = toResponsesInput(req.Messages)
		} else {
			payload["messages"] = toScopedMessages(req.Messages)
		}
	}
	applyCommonOptions(&payload, req.Options)

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

func toScopedMessages(msgs []chat.Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		if m.Content == "" {
			continue
		}
		out = append(out, map[string]any{
			"role":    m.Role,
			"content": m.Content,
		})
	}
	return out
}

func toResponsesInput(msgs []chat.Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		if m.Content == "" {
			continue
		}
		out = append(out, map[string]any{
			"role":    m.Role,
			"content": m.Content,
		})
	}
	return out
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
		fn, _ := call["function"].(map[string]any)
		out = append(out, chat.ToolCall{
			ID:   extractString(call, "id"),
			Type: extractString(call, "type"),
			Function: chat.ToolCallFunction{
				Name:      extractString(fn, "name"),
				Arguments: extractString(fn, "arguments"),
			},
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
