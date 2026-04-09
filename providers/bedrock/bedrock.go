package bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/bedrockruntime"
	"github.com/aws/aws-sdk-go/service/bedrockruntime/bedrockruntimeiface"
	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/diag"
	"github.com/quailyquaily/uniai/internal/httputil"
)

type Config struct {
	AwsKey    string
	AwsSecret string
	AwsRegion string
	ModelArn  string
	Headers   map[string]string
	Debug     bool
}

type Provider struct {
	client   bedrockruntimeiface.BedrockRuntimeAPI
	modelArn string
	headers  map[string]string
	debug    bool
}

func New(cfg Config) *Provider {
	region := cfg.AwsRegion
	if region == "" {
		region = "us-east-1"
	}
	sess := session.Must(session.NewSession(&aws.Config{
		Region:      aws.String(region),
		Credentials: credentials.NewStaticCredentials(cfg.AwsKey, cfg.AwsSecret, ""),
	}))
	return &Provider{
		client:   bedrockruntime.New(sess),
		modelArn: cfg.ModelArn,
		headers:  httputil.CloneHeaders(cfg.Headers),
		debug:    cfg.Debug,
	}
}

type bedrockMessage struct {
	Role    string              `json:"role"`
	Content []bedrockMsgContent `json:"content"`
}

type bedrockMsgContent struct {
	Type         string               `json:"type"`
	Text         string               `json:"text,omitempty"`
	CacheControl *bedrockCacheControl `json:"cache_control,omitempty"`
}

type bedrockResponse struct {
	Content []bedrockMsgContent `json:"content"`
	Usage   bedrockUsage        `json:"usage"`
}

type bedrockCacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type bedrockUsage struct {
	InputTokens              int            `json:"input_tokens,omitempty"`
	OutputTokens             int            `json:"output_tokens,omitempty"`
	CacheReadInputTokens     int            `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int            `json:"cache_creation_input_tokens,omitempty"`
	CacheWriteInputTokens    int            `json:"cache_write_input_tokens,omitempty"`
	CacheCreation            map[string]int `json:"cache_creation,omitempty"`
	CacheDetails             map[string]int `json:"cache_details,omitempty"`
}

func (p *Provider) Chat(ctx context.Context, req *chat.Request) (*chat.Result, error) {
	debugFn := req.Options.DebugFn
	if p.modelArn == "" {
		return nil, fmt.Errorf("bedrock model arn is required")
	}
	if err := validateBedrockCacheControl(req, p.modelArn); err != nil {
		return nil, err
	}

	systemParts := make([]string, 0, 1)
	messages := make([]bedrockMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case chat.RoleSystem:
			text, err := chat.MessageText(m)
			if err != nil {
				return nil, fmt.Errorf("bedrock provider model %q: role %q: %w", p.modelArn, m.Role, err)
			}
			if text != "" {
				systemParts = append(systemParts, text)
			}
		case chat.RoleUser, chat.RoleAssistant:
			content, err := toBedrockContent(m)
			if err != nil {
				return nil, fmt.Errorf("bedrock provider model %q: role %q: %w", p.modelArn, m.Role, err)
			}
			if len(content) == 0 {
				continue
			}
			messages = append(messages, bedrockMessage{
				Role:    m.Role,
				Content: content,
			})
		default:
			return nil, fmt.Errorf("bedrock provider does not support role %q", m.Role)
		}
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("at least one user or assistant message is required")
	}

	maxTokens := 10000
	if req.Options.MaxTokens != nil {
		maxTokens = *req.Options.MaxTokens
	}

	payload := map[string]any{
		"anthropic_version": "bedrock-2023-05-31",
		"max_tokens":        maxTokens,
		"messages":          messages,
	}
	if len(systemParts) > 0 {
		payload["system"] = strings.Join(systemParts, "\n")
	}
	applyBedrockOptions(payload, req.Options.Bedrock)

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	diag.LogText(p.debug, debugFn, "bedrock.chat.request", string(body))

	if req.Options.OnStream != nil {
		result, err := p.chatStream(ctx, body, req.Options.OnStream, req.Tools)
		if err != nil {
			diag.LogError(p.debug, debugFn, "bedrock.chat.response", err)
			return nil, err
		}
		return result, nil
	}

	resp, err := p.client.InvokeModelWithContext(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(p.modelArn),
		Body:        body,
		Accept:      aws.String("application/json"),
		ContentType: aws.String("application/json"),
	}, p.requestOptions()...)
	if err != nil {
		diag.LogError(p.debug, debugFn, "bedrock.chat.response", err)
		return nil, err
	}
	diag.LogText(p.debug, debugFn, "bedrock.chat.response", string(resp.Body))

	var out bedrockResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return nil, err
	}
	usage := parseBedrockUsage(resp.Body, out.Usage)

	var textParts []string
	for _, c := range out.Content {
		if c.Type == "text" && c.Text != "" {
			textParts = append(textParts, c.Text)
		}
	}
	text := strings.Join(textParts, "")

	result := &chat.Result{
		Text: text,
		Parts: func() []chat.Part {
			if text == "" {
				return nil
			}
			return []chat.Part{chat.TextPart(text)}
		}(),
		Usage: usage,
		Raw:   out,
	}
	if len(req.Tools) > 0 {
		result.Warnings = append(result.Warnings, "tools not supported for bedrock provider yet")
	}
	return result, nil
}

// bedrockStreamEvent represents a single event from the Bedrock streaming response.
// Each PayloadPart.Bytes contains a JSON object with a "type" field.
type bedrockStreamEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index,omitempty"`
	ContentBlock *struct {
		Type string `json:"type"`
	} `json:"content_block,omitempty"`
	Delta *struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"delta,omitempty"`
	Message *struct {
		Model string        `json:"model,omitempty"`
		Usage *bedrockUsage `json:"usage,omitempty"`
	} `json:"message,omitempty"`
	Usage *bedrockUsage `json:"usage,omitempty"`
}

func (p *Provider) chatStream(ctx context.Context, body []byte, onStream chat.OnStreamFunc, tools []chat.Tool) (*chat.Result, error) {
	resp, err := p.client.InvokeModelWithResponseStreamWithContext(ctx, &bedrockruntime.InvokeModelWithResponseStreamInput{
		ModelId:     aws.String(p.modelArn),
		Body:        body,
		Accept:      aws.String("application/json"),
		ContentType: aws.String("application/json"),
	}, p.requestOptions()...)
	if err != nil {
		return nil, err
	}
	stream := resp.GetStream()
	defer stream.Close()

	var (
		textParts []string
		model     string
		usage     chat.Usage
	)

	for event := range stream.Events() {
		chunk, ok := event.(*bedrockruntime.PayloadPart)
		if !ok || len(chunk.Bytes) == 0 {
			continue
		}

		var ev bedrockStreamEvent
		if err := json.Unmarshal(chunk.Bytes, &ev); err != nil {
			continue
		}

		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				model = ev.Message.Model
				if ev.Message.Usage != nil {
					applyBedrockUsage(&usage, *ev.Message.Usage)
				}
			}
		case "content_block_delta":
			if ev.Delta != nil && ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				textParts = append(textParts, ev.Delta.Text)
				if err := onStream(chat.StreamEvent{
					Delta: ev.Delta.Text,
				}); err != nil {
					return nil, err
				}
			}
		case "message_delta":
			if ev.Usage != nil {
				applyBedrockUsage(&usage, *ev.Usage)
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	if err := onStream(chat.StreamEvent{
		Done:  true,
		Usage: &usage,
	}); err != nil {
		return nil, err
	}

	result := &chat.Result{
		Text:  strings.Join(textParts, ""),
		Model: model,
		Parts: func() []chat.Part {
			text := strings.Join(textParts, "")
			if text == "" {
				return nil
			}
			return []chat.Part{chat.TextPart(text)}
		}(),
		Usage: usage,
	}
	if len(tools) > 0 {
		result.Warnings = append(result.Warnings, "tools not supported for bedrock provider yet")
	}
	return result, nil
}

func applyBedrockOptions(payload map[string]any, opts structs.JSONMap) {
	if payload == nil || len(opts) == 0 {
		return
	}
	opt := &opts
	if opt.HasKey("top_k") {
		if top := int(opt.GetInt64("top_k")); top > 0 {
			payload["top_k"] = top
		}
	}
}

func (p *Provider) requestOptions() []request.Option {
	if len(p.headers) == 0 {
		return nil
	}
	return []request.Option{request.WithSetRequestHeaders(p.headers)}
}

func validateBedrockCacheControl(req *chat.Request, modelArn string) error {
	if req == nil || !chat.RequestHasExplicitCacheControl(req) {
		return nil
	}
	if !strings.Contains(strings.ToLower(strings.TrimSpace(modelArn)), "anthropic.") {
		return fmt.Errorf("bedrock provider explicit cache control currently supports Anthropic Claude model arns only")
	}
	for i, msg := range req.Messages {
		if msg.Role != chat.RoleSystem {
			continue
		}
		for _, part := range msg.Parts {
			if part.CacheControl != nil {
				return fmt.Errorf("bedrock provider does not support explicit cache control on system parts yet (message[%d])", i)
			}
		}
	}
	for i, tool := range req.Tools {
		if tool.CacheControl != nil {
			return fmt.Errorf("bedrock provider does not support explicit cache control on tools yet (tool[%d])", i)
		}
	}
	return nil
}

func toBedrockContent(msg chat.Message) ([]bedrockMsgContent, error) {
	parts := chat.NormalizeMessageParts(msg)
	if len(parts) == 0 {
		return nil, nil
	}
	out := make([]bedrockMsgContent, 0, len(parts))
	for _, part := range parts {
		if err := chat.ValidatePart(part); err != nil {
			return nil, err
		}
		if part.Type != chat.PartTypeText {
			return nil, fmt.Errorf("unsupported part type %q", part.Type)
		}
		if strings.TrimSpace(part.Text) == "" && part.CacheControl == nil {
			continue
		}
		out = append(out, bedrockMsgContent{
			Type:         "text",
			Text:         part.Text,
			CacheControl: toBedrockCacheControl(part.CacheControl),
		})
	}
	return out, nil
}

func toBedrockCacheControl(ctrl *chat.CacheControl) *bedrockCacheControl {
	if ctrl == nil {
		return nil
	}
	out := &bedrockCacheControl{Type: "ephemeral"}
	if ttl := strings.TrimSpace(ctrl.TTL); ttl != "" {
		out.TTL = ttl
	}
	return out
}

func parseBedrockUsage(rawBody []byte, typed bedrockUsage) chat.Usage {
	usage := chat.Usage{
		InputTokens:  typed.InputTokens,
		OutputTokens: typed.OutputTokens,
		TotalTokens:  typed.InputTokens + typed.OutputTokens,
	}
	applyBedrockUsage(&usage, typed)

	var payload map[string]any
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		return usage
	}
	rawUsage, ok := payload["usage"].(map[string]any)
	if !ok {
		return usage
	}
	if usage.InputTokens == 0 {
		usage.InputTokens = int(extractBedrockNumber(rawUsage, "input_tokens"))
	}
	if usage.OutputTokens == 0 {
		usage.OutputTokens = int(extractBedrockNumber(rawUsage, "output_tokens"))
	}
	if usage.Cache.CachedInputTokens == 0 {
		usage.Cache.CachedInputTokens = int(extractBedrockNumber(rawUsage, "cache_read_input_tokens"))
	}
	if usage.Cache.CacheCreationInputTokens == 0 {
		usage.Cache.CacheCreationInputTokens = int(extractBedrockNumber(rawUsage, "cache_creation_input_tokens", "cache_write_input_tokens"))
	}
	chat.AddUsageCacheDetails(&usage, extractBedrockDetails(rawUsage, "cache_creation"))
	chat.AddUsageCacheDetails(&usage, extractBedrockDetails(rawUsage, "cache_details"))
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	return usage
}

func applyBedrockUsage(dst *chat.Usage, src bedrockUsage) {
	if dst == nil {
		return
	}
	if src.InputTokens > 0 {
		dst.InputTokens = src.InputTokens
	}
	if src.OutputTokens > 0 {
		dst.OutputTokens = src.OutputTokens
	}
	if src.CacheReadInputTokens > 0 {
		dst.Cache.CachedInputTokens = src.CacheReadInputTokens
	}
	switch {
	case src.CacheCreationInputTokens > 0:
		dst.Cache.CacheCreationInputTokens = src.CacheCreationInputTokens
	case src.CacheWriteInputTokens > 0:
		dst.Cache.CacheCreationInputTokens = src.CacheWriteInputTokens
	}
	chat.AddUsageCacheDetails(dst, src.CacheCreation)
	chat.AddUsageCacheDetails(dst, src.CacheDetails)
}

func extractBedrockNumber(m map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if raw, ok := m[key]; ok {
			switch val := raw.(type) {
			case float64:
				return val
			case float32:
				return float64(val)
			case int:
				return float64(val)
			case int32:
				return float64(val)
			case int64:
				return float64(val)
			}
		}
	}
	return 0
}

func extractBedrockDetails(m map[string]any, key string) map[string]int {
	raw, ok := m[key]
	if !ok {
		return nil
	}
	details, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]int, len(details))
	for name, val := range details {
		n := int(extractBedrockNumber(map[string]any{"value": val}, "value"))
		if n == 0 {
			continue
		}
		out[name] = n
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
