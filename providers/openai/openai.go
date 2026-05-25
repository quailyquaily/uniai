package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/diag"
	"github.com/quailyquaily/uniai/internal/httputil"
	"github.com/quailyquaily/uniai/internal/modelcompat"
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
		defaultModel: cfg.DefaultModel,
		debug:        cfg.Debug,
	}, nil
}

func (p *Provider) Chat(ctx context.Context, req *chat.Request) (*chat.Result, error) {
	debugFn := req.Options.DebugFn
	params, err := buildParams(req, p.defaultModel)
	if err != nil {
		return nil, err
	}
	diag.LogJSON(p.debug, debugFn, "openai.chat.request", params)

	if req.Options.OnStream != nil {
		streamDebug := newStreamRawErrorDebug(p.debug, debugFn, "openai.chat.stream.raw_error")
		var opts []option.RequestOption
		if streamDebug != nil {
			opts = append(opts, streamDebug.Option())
		}
		result, err := oaicompat.ChatStream(ctx, &p.client, params, req.Options.OnStream, opts...)
		if err != nil {
			streamDebug.Emit(err)
			diag.LogError(p.debug, debugFn, "openai.chat.response", err)
			return nil, err
		}
		return result, nil
	}

	result, raw, err := p.chatCompletion(ctx, params)
	if err != nil {
		diag.LogError(p.debug, debugFn, "openai.chat.response", err)
		return nil, err
	}
	if raw != "" {
		diag.LogText(p.debug, debugFn, "openai.chat.response", raw)
	} else {
		diag.LogJSON(p.debug, debugFn, "openai.chat.response", result.Raw)
	}
	return result, nil
}

func (p *Provider) chatCompletion(ctx context.Context, params openai.ChatCompletionNewParams) (*chat.Result, string, error) {
	var rawResp *http.Response
	if err := p.client.Execute(ctx, http.MethodPost, "chat/completions", params, &rawResp); err != nil {
		return nil, "", err
	}
	if rawResp == nil || rawResp.Body == nil {
		return nil, "", fmt.Errorf("openai chat response is empty")
	}
	defer rawResp.Body.Close()

	if oaicompat.IsEventStreamContentType(rawResp.Header.Get("Content-Type")) {
		result, err := oaicompat.ChatStreamFromResponse(rawResp, nil)
		return result, "", err
	}

	data, err := io.ReadAll(rawResp.Body)
	if err != nil {
		return nil, "", err
	}
	var resp openai.ChatCompletion
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, string(data), err
	}
	return toResult(&resp), string(data), nil
}

func buildParams(req *chat.Request, defaultModel string) (openai.ChatCompletionNewParams, error) {
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
	applyModelParameterOverlay(&params)

	return params, nil
}

func applyModelParameterOverlay(params *openai.ChatCompletionNewParams) {
	if params == nil {
		return
	}
	model := string(params.Model)
	if modelcompat.KimiK2UsesFixedSampling(model) {
		params.Temperature = param.Opt[float64]{}
		params.TopP = param.Opt[float64]{}
		params.N = param.Opt[int64]{}
		params.PresencePenalty = param.Opt[float64]{}
		params.FrequencyPenalty = param.Opt[float64]{}
	}
	if modelcompat.OpenAIGPT5DropsSampling(model, string(params.ReasoningEffort), params.ReasoningEffort != "") {
		params.Temperature = param.Opt[float64]{}
		params.TopP = param.Opt[float64]{}
		params.Logprobs = param.Opt[bool]{}
		params.TopLogprobs = param.Opt[int64]{}
	}
	if modelcompat.OpenAIRequires24hPromptCacheRetention(model) {
		current := params.ExtraFields()
		_, hasRetention := current["prompt_cache_retention"]
		if params.PromptCacheKey.Valid() || hasRetention {
			extra := map[string]any{}
			for key, value := range current {
				extra[key] = value
			}
			extra["prompt_cache_retention"] = "24h"
			params.SetExtraFields(extra)
		}
	}
}

func toResult(resp *openai.ChatCompletion) *chat.Result {
	return oaicompat.ChatCompletionToResult(resp)
}

func useMaxCompletionTokens(model string) bool {
	model = strings.ToLower(model)
	return strings.HasPrefix(model, "gpt") ||
		strings.HasPrefix(model, "o1") ||
		strings.HasPrefix(model, "o3") ||
		strings.HasPrefix(model, "o4")
}
