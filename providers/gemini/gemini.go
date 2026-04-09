package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/diag"
	"github.com/quailyquaily/uniai/internal/httputil"
	"github.com/quailyquaily/uniai/internal/toolschema"
)

const defaultGeminiAPIBase = "https://generativelanguage.googleapis.com"

type Config struct {
	APIKey       string
	BaseURL      string
	DefaultModel string
	Headers      map[string]string
	Debug        bool
}

type Provider struct {
	cfg Config
}

func New(cfg Config) (*Provider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("gemini api key is required")
	}
	cfg.Headers = httputil.CloneHeaders(cfg.Headers)
	return &Provider{cfg: cfg}, nil
}

type geminiRequest struct {
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	Contents          []geminiContent         `json:"contents"`
	Tools             []geminiTool            `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts,omitempty"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	InlineData       *geminiInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
}

type geminiFunctionCall struct {
	Name string `json:"name,omitempty"`
	Args any    `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string `json:"name,omitempty"`
	Response any    `json:"response,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type geminiFunctionDeclaration struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type geminiToolConfig struct {
	FunctionCallingConfig *geminiFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

type geminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature      *float64              `json:"temperature,omitempty"`
	TopP             *float64              `json:"topP,omitempty"`
	TopK             *int                  `json:"topK,omitempty"`
	MaxOutputTokens  *int                  `json:"maxOutputTokens,omitempty"`
	StopSequences    []string              `json:"stopSequences,omitempty"`
	CandidateCount   *int                  `json:"candidateCount,omitempty"`
	ResponseMIMEType string                `json:"responseMimeType,omitempty"`
	ResponseSchema   any                   `json:"responseSchema,omitempty"`
	ThinkingConfig   *geminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

type geminiThinkingConfig struct {
	IncludeThoughts *bool  `json:"includeThoughts,omitempty"`
	ThinkingBudget  *int   `json:"thinkingBudget,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
}

type geminiResponse struct {
	Candidates []geminiCandidate `json:"candidates,omitempty"`
	Usage      geminiUsage       `json:"usageMetadata,omitempty"`
	Model      string            `json:"modelVersion,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content,omitempty"`
	FinishReason string        `json:"finishReason,omitempty"`
}

type geminiUsage struct {
	InputTokens    int `json:"promptTokenCount,omitempty"`
	OutputTokens   int `json:"candidatesTokenCount,omitempty"`
	TotalTokens    int `json:"totalTokenCount,omitempty"`
	ThoughtsTokens int `json:"thoughtsTokenCount,omitempty"`
}

type geminiErrorEnvelope struct {
	Error struct {
		Message string `json:"message,omitempty"`
	} `json:"error"`
}

func (p *Provider) Chat(ctx context.Context, req *chat.Request) (*chat.Result, error) {
	debugFn := req.Options.DebugFn
	if req.Options.OnStream != nil {
		return nil, fmt.Errorf("gemini provider does not support streaming yet")
	}
	if err := chat.ValidateNoScopedCacheControl(req, "gemini"); err != nil {
		return nil, err
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = strings.TrimSpace(p.cfg.DefaultModel)
	}
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}

	payload, err := buildRequest(req, model)
	if err != nil {
		return nil, fmt.Errorf("gemini provider model %q: %w", model, err)
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	diag.LogText(p.cfg.Debug, debugFn, "gemini.chat.request", string(reqBody))

	base := normalizeGeminiBase(p.cfg.BaseURL)
	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s",
		base,
		url.PathEscape(normalizeGeminiModel(model)),
		url.QueryEscape(p.cfg.APIKey),
	)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httputil.ApplyHeaders(httpReq.Header, p.cfg.Headers)

	resp, err := httputil.ClientForContext(ctx).Do(httpReq)
	if err != nil {
		diag.LogError(p.cfg.Debug, debugFn, "gemini.chat.response", err)
		return nil, err
	}
	defer resp.Body.Close()

	respData, err := httputil.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}
	diag.LogText(p.cfg.Debug, debugFn, "gemini.chat.response", string(respData))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini api error: status %d: %s", resp.StatusCode, parseGeminiError(respData))
	}

	var out geminiResponse
	if err := json.Unmarshal(respData, &out); err != nil {
		return nil, err
	}

	result, err := toChatResult(&out, model, req.Options.ReasoningDetails)
	if err != nil {
		return nil, err
	}
	result.Raw = out
	return result, nil
}

func buildRequest(req *chat.Request, model string) (*geminiRequest, error) {
	out := &geminiRequest{}

	systemParts := make([]geminiPart, 0, 1)
	contents := make([]geminiContent, 0, len(req.Messages))
	callNameByID := map[string]string{}

	for _, msg := range req.Messages {
		switch msg.Role {
		case chat.RoleSystem:
			for _, part := range chat.NormalizeMessageParts(msg) {
				if err := chat.ValidatePart(part); err != nil {
					return nil, fmt.Errorf("role %q: %w", msg.Role, err)
				}
				if part.Type != chat.PartTypeText {
					return nil, fmt.Errorf("role %q: unsupported part type %q", msg.Role, part.Type)
				}
				if strings.TrimSpace(part.Text) != "" {
					systemParts = append(systemParts, geminiPart{Text: part.Text})
				}
			}
		case chat.RoleUser:
			for _, part := range chat.NormalizeMessageParts(msg) {
				if err := chat.ValidatePart(part); err != nil {
					return nil, fmt.Errorf("role %q: %w", msg.Role, err)
				}
				switch part.Type {
				case chat.PartTypeText:
					if strings.TrimSpace(part.Text) == "" {
						continue
					}
					appendContent(&contents, "user", geminiPart{Text: part.Text})
				case chat.PartTypeImageBase64:
					mimeType := strings.TrimSpace(part.MIMEType)
					if mimeType == "" {
						mimeType = "image/png"
					}
					appendContent(&contents, "user", geminiPart{
						InlineData: &geminiInlineData{
							MimeType: mimeType,
							Data:     strings.TrimSpace(part.DataBase64),
						},
					})
				case chat.PartTypeImageURL:
					return nil, fmt.Errorf("role %q: unsupported part type %q", msg.Role, part.Type)
				default:
					return nil, fmt.Errorf("role %q: unsupported part type %q", msg.Role, part.Type)
				}
			}
		case chat.RoleAssistant:
			assistantParts := make([]geminiPart, 0, 1+len(msg.ToolCalls))
			for _, part := range chat.NormalizeMessageParts(msg) {
				if err := chat.ValidatePart(part); err != nil {
					return nil, fmt.Errorf("role %q: %w", msg.Role, err)
				}
				if part.Type != chat.PartTypeText {
					return nil, fmt.Errorf("role %q: unsupported part type %q", msg.Role, part.Type)
				}
				if strings.TrimSpace(part.Text) != "" {
					assistantParts = append(assistantParts, geminiPart{Text: part.Text})
				}
			}
			for _, call := range msg.ToolCalls {
				if call.Type != "" && call.Type != "function" {
					continue
				}
				if strings.TrimSpace(call.Function.Name) == "" {
					return nil, fmt.Errorf("assistant tool call name is required")
				}
				callID := strings.TrimSpace(call.ID)
				sig := strings.TrimSpace(call.ThoughtSignature)
				normalizedCallID := callID
				if sig == "" && callID != "" {
					normalizedID, decoded := splitToolCallIDAndThoughtSignature(callID)
					if decoded != "" {
						sig = decoded
						normalizedCallID = normalizedID
					}
				}
				if sig == "" {
					return nil, fmt.Errorf("assistant tool call %q (id=%q) is missing thought_signature; preserve prior resp.ToolCalls as-is when sending tool results", call.Function.Name, call.ID)
				}
				args := parseFunctionArgs(call.Function.Arguments)
				part := geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: call.Function.Name,
						Args: args,
					},
					ThoughtSignature: sig,
				}
				if callID != "" {
					callNameByID[callID] = call.Function.Name
				}
				if normalizedCallID != "" {
					callNameByID[normalizedCallID] = call.Function.Name
				}
				assistantParts = append(assistantParts, part)
			}
			for _, part := range assistantParts {
				appendContent(&contents, "model", part)
			}
		case chat.RoleTool:
			if msg.ToolCallID == "" {
				return nil, fmt.Errorf("tool_call_id is required for tool messages")
			}
			contentText, err := chat.MessageText(msg)
			if err != nil {
				return nil, fmt.Errorf("role %q: %w", msg.Role, err)
			}
			name := callNameByID[msg.ToolCallID]
			if name == "" {
				return nil, fmt.Errorf("tool message references unknown tool_call_id: %s", msg.ToolCallID)
			}
			appendContent(&contents, "user", geminiPart{
				FunctionResponse: &geminiFunctionResponse{
					Name:     name,
					Response: parseFunctionResponse(contentText),
				},
			})
		default:
			return nil, fmt.Errorf("gemini provider does not support role %q", msg.Role)
		}
	}

	if len(systemParts) > 0 {
		out.SystemInstruction = &geminiContent{Parts: systemParts}
	}
	if len(contents) == 0 {
		return nil, fmt.Errorf("at least one non-system message is required")
	}
	out.Contents = contents

	if len(req.Tools) > 0 {
		tools, err := toGeminiTools(req.Tools)
		if err != nil {
			return nil, err
		}
		if len(tools) > 0 {
			out.Tools = tools
		}
	}
	if len(out.Tools) > 0 && req.ToolChoice != nil {
		cfg, err := toFunctionCallingConfig(req.ToolChoice)
		if err != nil {
			return nil, err
		}
		if cfg != nil {
			out.ToolConfig = &geminiToolConfig{FunctionCallingConfig: cfg}
		}
	}

	gen, err := toGenerationConfig(model, req.Options)
	if err != nil {
		return nil, err
	}
	if gen != nil {
		out.GenerationConfig = gen
	}

	return out, nil
}

func appendContent(contents *[]geminiContent, role string, part geminiPart) {
	if contents == nil {
		return
	}
	if len(*contents) == 0 || (*contents)[len(*contents)-1].Role != role {
		*contents = append(*contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{part},
		})
		return
	}
	last := &(*contents)[len(*contents)-1]
	last.Parts = append(last.Parts, part)
}

func parseFunctionArgs(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}
	var out any
	if err := json.Unmarshal([]byte(raw), &out); err == nil {
		return out
	}
	return map[string]any{"raw": raw}
}

func parseFunctionResponse(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}
	var out any
	if err := json.Unmarshal([]byte(raw), &out); err == nil {
		return out
	}
	return map[string]any{"content": raw}
}

func toGeminiTools(tools []chat.Tool) ([]geminiTool, error) {
	decls := make([]geminiFunctionDeclaration, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "function" {
			continue
		}
		if strings.TrimSpace(tool.Function.Name) == "" {
			continue
		}

		decl := geminiFunctionDeclaration{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
		}
		if len(tool.Function.ParametersJSONSchema) > 0 {
			var schema map[string]any
			if err := json.Unmarshal(tool.Function.ParametersJSONSchema, &schema); err != nil {
				return nil, err
			}
			toolschema.Normalize(schema)
			decl.Parameters = toGeminiSchema(schema)
		} else {
			decl.Parameters = map[string]any{
				"type":       "OBJECT",
				"properties": map[string]any{},
			}
		}
		decls = append(decls, decl)
	}

	if len(decls) == 0 {
		return nil, nil
	}
	return []geminiTool{{FunctionDeclarations: decls}}, nil
}

func toGeminiSchema(in any) any {
	switch node := in.(type) {
	case map[string]any:
		out := make(map[string]any, len(node))
		for k, v := range node {
			if shouldDropGeminiSchemaKey(k) {
				continue
			}
			out[k] = toGeminiSchema(v)
		}
		typeNames, nullable := normalizeTypeValue(node["type"])
		if len(typeNames) > 0 {
			out["type"] = typeNames[0]
		}
		if nullable {
			out["nullable"] = true
		}
		if _, ok := out["type"]; !ok {
			if _, hasProps := out["properties"]; hasProps {
				out["type"] = "OBJECT"
			} else if _, hasItems := out["items"]; hasItems {
				out["type"] = "ARRAY"
			}
		}
		if t, _ := out["type"].(string); t == "ARRAY" {
			if _, ok := out["items"]; !ok {
				out["items"] = map[string]any{}
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(node))
		for _, item := range node {
			out = append(out, toGeminiSchema(item))
		}
		return out
	default:
		return in
	}
}

func shouldDropGeminiSchemaKey(key string) bool {
	switch key {
	case "additionalProperties":
		return true
	default:
		return false
	}
}

func normalizeTypeValue(raw any) ([]string, bool) {
	switch t := raw.(type) {
	case string:
		upper := strings.ToUpper(strings.TrimSpace(t))
		if upper == "NULL" {
			return nil, true
		}
		if upper == "" {
			return nil, false
		}
		return []string{upper}, false
	case []string:
		names := make([]string, 0, len(t))
		nullable := false
		for _, item := range t {
			upper := strings.ToUpper(strings.TrimSpace(item))
			if upper == "" {
				continue
			}
			if upper == "NULL" {
				nullable = true
				continue
			}
			names = append(names, upper)
		}
		return names, nullable
	case []any:
		names := make([]string, 0, len(t))
		nullable := false
		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				continue
			}
			upper := strings.ToUpper(strings.TrimSpace(s))
			if upper == "" {
				continue
			}
			if upper == "NULL" {
				nullable = true
				continue
			}
			names = append(names, upper)
		}
		return names, nullable
	default:
		return nil, false
	}
}

func toFunctionCallingConfig(choice *chat.ToolChoice) (*geminiFunctionCallingConfig, error) {
	if choice == nil {
		return nil, nil
	}
	switch choice.Mode {
	case "", "auto":
		return &geminiFunctionCallingConfig{Mode: "AUTO"}, nil
	case "none":
		return &geminiFunctionCallingConfig{Mode: "NONE"}, nil
	case "required":
		return &geminiFunctionCallingConfig{Mode: "ANY"}, nil
	case "function":
		name := strings.TrimSpace(choice.FunctionName)
		if name == "" {
			return nil, fmt.Errorf("tool_choice function_name is required when mode=function")
		}
		return &geminiFunctionCallingConfig{
			Mode:                 "ANY",
			AllowedFunctionNames: []string{name},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported tool_choice mode %q", choice.Mode)
	}
}

func toGenerationConfig(model string, opts chat.Options) (*geminiGenerationConfig, error) {
	cfg := &geminiGenerationConfig{}
	has := false

	if opts.Temperature != nil {
		cfg.Temperature = opts.Temperature
		has = true
	}
	if opts.TopP != nil {
		cfg.TopP = opts.TopP
		has = true
	}
	if opts.MaxTokens != nil {
		cfg.MaxOutputTokens = opts.MaxTokens
		has = true
	}
	if len(opts.Stop) > 0 {
		cfg.StopSequences = append([]string{}, opts.Stop...)
		has = true
	}

	applyGeminiOptions(cfg, opts.OpenAI)
	applyGeminiOptions(cfg, opts.Azure)
	applyGeminiOptions(cfg, opts.Cloudflare)
	if err := applyGeminiReasoningOptions(model, cfg, opts); err != nil {
		return nil, err
	}

	if !has {
		if cfg.TopK == nil && cfg.CandidateCount == nil && cfg.ResponseMIMEType == "" && cfg.ResponseSchema == nil && cfg.ThinkingConfig == nil {
			return nil, nil
		}
	}
	return cfg, nil
}

func applyGeminiReasoningOptions(model string, cfg *geminiGenerationConfig, opts chat.Options) error {
	if cfg == nil {
		return nil
	}
	if opts.ReasoningEffort == nil && opts.ReasoningBudget == nil && !opts.ReasoningDetails {
		return nil
	}
	if opts.ReasoningEffort != nil && opts.ReasoningBudget != nil {
		return fmt.Errorf("gemini provider does not support reasoning effort and reasoning budget tokens together")
	}

	model = strings.ToLower(normalizeGeminiModel(model))
	thinking := cfg.ThinkingConfig
	if thinking == nil {
		thinking = &geminiThinkingConfig{}
	}
	if opts.ReasoningDetails {
		thinking.IncludeThoughts = boolPtr(true)
	}

	switch {
	case strings.HasPrefix(model, "gemini-3"):
		if opts.ReasoningBudget != nil {
			return fmt.Errorf("gemini model %q uses reasoning effort, not reasoning budget tokens", model)
		}
		if opts.ReasoningEffort != nil {
			level, err := geminiThinkingLevelForEffort(*opts.ReasoningEffort)
			if err != nil {
				return err
			}
			thinking.ThinkingLevel = level
		}
	case strings.HasPrefix(model, "gemini-2.5"):
		if opts.ReasoningBudget != nil {
			if err := validateGemini25ReasoningBudget(model, *opts.ReasoningBudget); err != nil {
				return err
			}
			thinking.ThinkingBudget = opts.ReasoningBudget
		}
		if opts.ReasoningEffort != nil {
			budget, err := gemini25BudgetForEffort(*opts.ReasoningEffort)
			if err != nil {
				return err
			}
			if err := validateGemini25ReasoningBudget(model, budget); err != nil {
				return err
			}
			thinking.ThinkingBudget = intPtr(budget)
		}
	default:
		return fmt.Errorf("gemini provider model %q does not support unified reasoning controls", model)
	}

	if thinking.IncludeThoughts != nil || thinking.ThinkingBudget != nil || strings.TrimSpace(thinking.ThinkingLevel) != "" {
		cfg.ThinkingConfig = thinking
	}
	return nil
}

func geminiThinkingLevelForEffort(effort chat.ReasoningEffort) (string, error) {
	switch effort {
	case chat.ReasoningEffortMinimal:
		return "minimal", nil
	case chat.ReasoningEffortLow:
		return "low", nil
	case chat.ReasoningEffortMedium:
		return "medium", nil
	case chat.ReasoningEffortHigh:
		return "high", nil
	default:
		return "", fmt.Errorf("gemini provider does not support reasoning effort %q", effort)
	}
}

func gemini25BudgetForEffort(effort chat.ReasoningEffort) (int, error) {
	switch effort {
	case chat.ReasoningEffortNone:
		return 0, nil
	case chat.ReasoningEffortMinimal:
		return 1024, nil
	case chat.ReasoningEffortLow:
		return 4096, nil
	case chat.ReasoningEffortMedium:
		return 16384, nil
	case chat.ReasoningEffortHigh:
		return 24576, nil
	default:
		return 0, fmt.Errorf("gemini 2.5 provider does not support reasoning effort %q", effort)
	}
}

func validateGemini25ReasoningBudget(model string, budget int) error {
	switch {
	case strings.Contains(model, "flash-lite"):
		if budget == -1 || budget == 0 {
			return nil
		}
		if budget < 512 || budget > 24576 {
			return fmt.Errorf("gemini model %q reasoning budget must be -1, 0, or 512..24576", model)
		}
		return nil
	case strings.Contains(model, "flash"):
		if budget == -1 {
			return nil
		}
		if budget < 0 || budget > 24576 {
			return fmt.Errorf("gemini model %q reasoning budget must be -1 or 0..24576", model)
		}
		return nil
	default:
		if budget == -1 {
			return nil
		}
		if budget < 128 || budget > 32768 {
			return fmt.Errorf("gemini model %q reasoning budget must be -1 or 128..32768", model)
		}
		return nil
	}
}

func applyGeminiOptions(cfg *geminiGenerationConfig, opts structs.JSONMap) {
	if cfg == nil || len(opts) == 0 {
		return
	}
	opt := &opts
	if opt.HasKey("top_k") && cfg.TopK == nil {
		if topK := int(opt.GetInt64("top_k")); topK > 0 {
			cfg.TopK = &topK
		}
	}
	if opt.HasKey("n") && cfg.CandidateCount == nil {
		if n := int(opt.GetInt64("n")); n > 0 {
			cfg.CandidateCount = &n
		}
	}
	if opt.HasKey("candidate_count") && cfg.CandidateCount == nil {
		if n := int(opt.GetInt64("candidate_count")); n > 0 {
			cfg.CandidateCount = &n
		}
	}
	if opt.HasKey("response_mime_type") && cfg.ResponseMIMEType == "" {
		if v := strings.TrimSpace(opt.GetString("response_mime_type")); v != "" {
			cfg.ResponseMIMEType = v
		}
	}
	if opt.HasKey("response_schema") && cfg.ResponseSchema == nil {
		if schemaMap := opt.GetMap("response_schema"); schemaMap != nil && len(*schemaMap) > 0 {
			cfg.ResponseSchema = toGeminiSchema(map[string]any(*schemaMap))
			return
		}
		if schemaArray := opt.GetArray("response_schema"); len(schemaArray) > 0 {
			cfg.ResponseSchema = toGeminiSchema(schemaArray)
			return
		}
		if schemaJSON := strings.TrimSpace(opt.GetString("response_schema")); schemaJSON != "" {
			var schemaAny any
			if err := json.Unmarshal([]byte(schemaJSON), &schemaAny); err == nil {
				cfg.ResponseSchema = toGeminiSchema(schemaAny)
			}
		}
	}
}

func toChatResult(in *geminiResponse, fallbackModel string, reasoningDetails bool) (*chat.Result, error) {
	if in == nil {
		return &chat.Result{Model: fallbackModel}, nil
	}
	result := &chat.Result{
		Model: in.Model,
		Usage: chat.Usage{
			InputTokens:  in.Usage.InputTokens,
			OutputTokens: in.Usage.OutputTokens,
			TotalTokens:  in.Usage.TotalTokens,
		},
	}
	if result.Model == "" {
		result.Model = fallbackModel
	}

	if len(in.Candidates) == 0 {
		return result, nil
	}

	parts := in.Candidates[0].Content.Parts
	text := make([]string, 0, len(parts))
	outParts := make([]chat.Part, 0, len(parts))
	calls := make([]chat.ToolCall, 0)
	for i, part := range parts {
		if strings.TrimSpace(part.Text) != "" {
			if part.Thought {
				if reasoningDetails {
					result.Reasoning = appendGeminiReasoningDetail(result.Reasoning, part.Text)
				}
			} else {
				text = append(text, part.Text)
				outParts = append(outParts, chat.TextPart(part.Text))
			}
		}
		if part.FunctionCall != nil {
			args, err := json.Marshal(part.FunctionCall.Args)
			if err != nil {
				return nil, err
			}
			if len(args) == 0 || string(args) == "null" {
				args = []byte("{}")
			}
			signature := strings.TrimSpace(part.ThoughtSignature)
			calls = append(calls, chat.ToolCall{
				ID:   encodeToolCallID(fmt.Sprintf("call_%d", i+1), signature),
				Type: "function",
				Function: chat.ToolCallFunction{
					Name:      part.FunctionCall.Name,
					Arguments: string(args),
				},
				ThoughtSignature: signature,
			})
		}
	}
	result.Text = strings.Join(text, "")
	result.Parts = outParts
	result.ToolCalls = calls
	return result, nil
}

func appendGeminiReasoningDetail(reasoning *chat.ReasoningResult, text string) *chat.ReasoningResult {
	text = strings.TrimSpace(text)
	if text == "" {
		return reasoning
	}
	if reasoning == nil {
		reasoning = &chat.ReasoningResult{}
	}
	reasoning.Summary = append(reasoning.Summary, text)
	return reasoning
}

func boolPtr(v bool) *bool { return &v }

func intPtr(v int) *int { return &v }

func encodeToolCallID(callID, thoughtSignature string) string {
	callID = strings.TrimSpace(callID)
	thoughtSignature = strings.TrimSpace(thoughtSignature)
	if callID == "" || thoughtSignature == "" {
		return callID
	}
	return callID + "|ts:" + base64.RawURLEncoding.EncodeToString([]byte(thoughtSignature))
}

func splitToolCallIDAndThoughtSignature(callID string) (string, string) {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return "", ""
	}
	idx := strings.LastIndex(callID, "|ts:")
	if idx <= 0 || idx+4 >= len(callID) {
		return callID, ""
	}
	encoded := callID[idx+4:]
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return callID, ""
	}
	baseID := callID[:idx]
	if strings.TrimSpace(baseID) == "" {
		return callID, ""
	}
	return baseID, string(decoded)
}

func parseGeminiError(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	var env geminiErrorEnvelope
	if err := json.Unmarshal(data, &env); err == nil {
		if msg := strings.TrimSpace(env.Error.Message); msg != "" {
			return msg
		}
	}
	return strings.TrimSpace(string(data))
}

func normalizeGeminiBase(base string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(base), "/")
	if trimmed == "" {
		return defaultGeminiAPIBase
	}
	trimmed = strings.TrimSuffix(trimmed, "/v1beta/openai")
	trimmed = strings.TrimSuffix(trimmed, "/v1/openai")
	trimmed = strings.TrimSuffix(trimmed, "/openai")
	trimmed = strings.TrimSuffix(trimmed, "/v1beta")
	trimmed = strings.TrimSuffix(trimmed, "/v1")
	if trimmed == "" {
		return defaultGeminiAPIBase
	}
	return trimmed
}

func normalizeGeminiModel(model string) string {
	model = strings.TrimSpace(model)
	model = strings.TrimPrefix(model, "models/")
	return model
}
