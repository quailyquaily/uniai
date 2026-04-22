package openai

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/diag"
	"github.com/quailyquaily/uniai/internal/httputil"
	"github.com/quailyquaily/uniai/internal/oaicompat"
)

type Config struct {
	APIKey       string
	BaseURL      string
	DefaultModel string
	Headers      map[string]string
	Debug        bool
}

type Provider struct {
	client       openai.Client
	baseURL      string
	defaultModel string
	debug        bool
}

func New(cfg Config) (*Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("openai api key is required")
	}

	opts := []option.RequestOption{option.WithAPIKey(cfg.APIKey)}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	for key, value := range httputil.CloneHeaders(cfg.Headers) {
		opts = append(opts, option.WithHeader(key, value))
	}
	return &Provider{
		client:       openai.NewClient(opts...),
		baseURL:      strings.TrimSpace(cfg.BaseURL),
		defaultModel: cfg.DefaultModel,
		debug:        cfg.Debug,
	}, nil
}

func (p *Provider) Chat(ctx context.Context, req *chat.Request) (*chat.Result, error) {
	debugFn := req.Options.DebugFn
	params, err := buildParams(req, p.defaultModel, p.baseURL)
	if err != nil {
		return nil, err
	}
	diag.LogJSON(p.debug, debugFn, "openai.chat.request", params)

	if req.Options.OnStream != nil {
		result, err := oaicompat.ChatStream(ctx, &p.client, params, req.Options.OnStream)
		if err != nil {
			diag.LogError(p.debug, debugFn, "openai.chat.response", err)
			return nil, err
		}
		return result, nil
	}

	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		diag.LogError(p.debug, debugFn, "openai.chat.response", err)
		return nil, err
	}
	if raw := resp.RawJSON(); raw != "" {
		diag.LogText(p.debug, debugFn, "openai.chat.response", raw)
	} else {
		diag.LogJSON(p.debug, debugFn, "openai.chat.response", resp)
	}
	return toResult(resp), nil
}

func buildParams(req *chat.Request, defaultModel string, baseURL ...string) (openai.ChatCompletionNewParams, error) {
	model := req.Model
	if model == "" {
		model = defaultModel
	}
	if model == "" {
		return openai.ChatCompletionNewParams{}, fmt.Errorf("model is required")
	}
	if req.Options.ReasoningBudget != nil {
		return openai.ChatCompletionNewParams{}, fmt.Errorf("openai provider does not support reasoning budget tokens; use reasoning effort")
	}
	if req.Options.ReasoningDetails {
		return openai.ChatCompletionNewParams{}, fmt.Errorf("openai provider reasoning details require a Responses API path; chat completions are not supported yet")
	}
	if err := chat.ValidateNoScopedCacheControl(req, "openai"); err != nil {
		return openai.ChatCompletionNewParams{}, err
	}

	messages, err := oaicompat.ToMessages(req.Messages, model)
	if err != nil {
		return openai.ChatCompletionNewParams{}, fmt.Errorf("openai provider model %q: %w", model, err)
	}
	applyKimiReasoningContentWorkaround(messages, model, firstNonEmpty(baseURL...))

	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(model),
		Messages: messages,
	}

	if req.Options.Temperature != nil {
		params.Temperature = openai.Float(*req.Options.Temperature)
	}
	if req.Options.TopP != nil {
		params.TopP = openai.Float(*req.Options.TopP)
	}
	if req.Options.MaxTokens != nil {
		maxTokens := int64(*req.Options.MaxTokens)
		if useMaxCompletionTokens(model) {
			params.MaxCompletionTokens = openai.Int(maxTokens)
		} else {
			params.MaxTokens = openai.Int(maxTokens)
		}
	}
	if len(req.Options.Stop) > 0 {
		params.Stop = openai.ChatCompletionNewParamsStopUnion{
			OfStringArray: append([]string{}, req.Options.Stop...),
		}
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
	if req.Options.ReasoningEffort != nil {
		params.ReasoningEffort = shared.ReasoningEffort(*req.Options.ReasoningEffort)
	}

	if len(req.Tools) > 0 {
		tools, err := oaicompat.ToToolParams(req.Tools)
		if err != nil {
			return openai.ChatCompletionNewParams{}, err
		}
		params.Tools = tools
	}

	if req.ToolChoice != nil {
		params.ToolChoice = oaicompat.ToToolChoice(req.ToolChoice)
	}

	oaicompat.ApplyOptions(&params, req.Options.OpenAI)

	return params, nil
}

func toResult(resp *openai.ChatCompletion) *chat.Result {
	if resp == nil {
		return &chat.Result{Warnings: []string{"openai response is nil"}}
	}
	text := ""
	parts := make([]chat.Part, 0, 1)
	var toolCalls []chat.ToolCall
	for _, choice := range resp.Choices {
		text += choice.Message.Content
		if len(choice.Message.ToolCalls) > 0 && len(toolCalls) == 0 {
			toolCalls = oaicompat.ToToolCalls(choice.Message.ToolCalls)
		}
	}
	if text != "" {
		parts = append(parts, chat.TextPart(text))
	}

	return &chat.Result{
		Text:      text,
		Parts:     parts,
		Model:     resp.Model,
		ToolCalls: toolCalls,
		Usage:     oaicompat.ChatCompletionUsageToChatUsage(resp.Usage),
		Raw:       resp,
	}
}

func useMaxCompletionTokens(model string) bool {
	model = strings.ToLower(model)
	return strings.HasPrefix(model, "gpt") ||
		strings.HasPrefix(model, "o1") ||
		strings.HasPrefix(model, "o3") ||
		strings.HasPrefix(model, "o4")
}

func applyKimiReasoningContentWorkaround(messages []openai.ChatCompletionMessageParamUnion, model, baseURL string) {
	if !shouldApplyKimiReasoningContentWorkaround(model, baseURL) {
		return
	}
	for i := range messages {
		msg := messages[i].OfAssistant
		if msg == nil || len(msg.ToolCalls) == 0 {
			continue
		}
		msg.SetExtraFields(map[string]any{
			"reasoning_content": ".",
		})
	}
}

func shouldApplyKimiReasoningContentWorkaround(model, baseURL string) bool {
	normalizedModel := strings.ToLower(strings.TrimSpace(model))
	normalizedModel = strings.TrimPrefix(normalizedModel, "models/")
	if strings.HasPrefix(normalizedModel, "kimi-") {
		return true
	}
	return isKimiCompatibleBaseURL(baseURL)
}

func isKimiCompatibleBaseURL(baseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return false
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "api.moonshot.ai", "api.kimi.com", "api.moonshot.cn":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
