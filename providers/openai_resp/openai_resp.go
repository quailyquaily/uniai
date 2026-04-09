package openairesp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/lyricat/goutils/structs"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/diag"
	"github.com/quailyquaily/uniai/internal/httputil"
	"github.com/quailyquaily/uniai/internal/oaicompat"
	"github.com/quailyquaily/uniai/internal/toolschema"
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
	if strings.TrimSpace(cfg.APIKey) == "" {
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
	diag.LogJSON(p.debug, debugFn, "openai.responses.request", params)

	if req.Options.OnStream != nil {
		result, err := streamChat(ctx, &p.client, params, req.Options.OnStream)
		if err != nil {
			diag.LogError(p.debug, debugFn, "openai.responses.response", err)
			return nil, err
		}
		return result, nil
	}

	resp, err := p.client.Responses.New(ctx, params)
	if err != nil {
		diag.LogError(p.debug, debugFn, "openai.responses.response", err)
		return nil, err
	}
	if raw := resp.RawJSON(); raw != "" {
		diag.LogText(p.debug, debugFn, "openai.responses.response", raw)
	} else {
		diag.LogJSON(p.debug, debugFn, "openai.responses.response", resp)
	}

	if err := responseStatusError(resp); err != nil {
		return nil, err
	}

	return toResult(resp), nil
}

var openAIResponsesOptionKeys = map[string]struct{}{
	"background":             {},
	"conversation":           {},
	"include":                {},
	"input":                  {},
	"instructions":           {},
	"max_tool_calls":         {},
	"metadata":               {},
	"parallel_tool_calls":    {},
	"previous_response_id":   {},
	"prompt":                 {},
	"prompt_cache_key":       {},
	"prompt_cache_retention": {},
	"reasoning":              {},
	"response_format":        {},
	"safety_identifier":      {},
	"service_tier":           {},
	"store":                  {},
	"stream_options":         {},
	"text":                   {},
	"tool_choice":            {},
	"tools":                  {},
	"top_logprobs":           {},
	"truncation":             {},
	"user":                   {},
	"verbosity":              {},
}

func buildParams(req *chat.Request, defaultModel string) (responses.ResponseNewParams, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = strings.TrimSpace(defaultModel)
	}
	if model == "" {
		return responses.ResponseNewParams{}, fmt.Errorf("model is required")
	}
	if req.Options.ReasoningBudget != nil {
		return responses.ResponseNewParams{}, fmt.Errorf("openai_resp provider does not support reasoning budget tokens; use reasoning effort")
	}
	if len(req.Options.Stop) > 0 {
		return responses.ResponseNewParams{}, fmt.Errorf("openai_resp provider does not support stop sequences on the Responses API")
	}
	if req.Options.PresencePenalty != nil {
		return responses.ResponseNewParams{}, fmt.Errorf("openai_resp provider does not support presence penalty on the Responses API")
	}
	if req.Options.FrequencyPenalty != nil {
		return responses.ResponseNewParams{}, fmt.Errorf("openai_resp provider does not support frequency penalty on the Responses API")
	}
	if err := chat.ValidateNoScopedCacheControl(req, "openai_resp"); err != nil {
		return responses.ResponseNewParams{}, err
	}

	opts := req.Options.OpenAI
	if err := validateOpenAIResponsesOptions(opts); err != nil {
		return responses.ResponseNewParams{}, err
	}
	if err := validateOpenAIResponsesConflicts(req, opts); err != nil {
		return responses.ResponseNewParams{}, err
	}

	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(model),
	}

	if req.Options.Temperature != nil {
		params.Temperature = openai.Float(*req.Options.Temperature)
	}
	if req.Options.TopP != nil {
		params.TopP = openai.Float(*req.Options.TopP)
	}
	if req.Options.MaxTokens != nil {
		params.MaxOutputTokens = openai.Int(int64(*req.Options.MaxTokens))
	}
	if req.Options.User != nil {
		params.User = openai.String(*req.Options.User)
	}

	if err := applyRawRootOptions(&params, opts); err != nil {
		return responses.ResponseNewParams{}, err
	}

	if opts.HasKey("reasoning") {
		reasoning, err := decodeJSONValue[shared.ReasoningParam](opts["reasoning"])
		if err != nil {
			return responses.ResponseNewParams{}, fmt.Errorf("openai reasoning: %w", err)
		}
		params.Reasoning = reasoning
	} else if req.Options.ReasoningEffort != nil || req.Options.ReasoningDetails {
		reasoning := shared.ReasoningParam{}
		if req.Options.ReasoningEffort != nil {
			reasoning.Effort = shared.ReasoningEffort(*req.Options.ReasoningEffort)
		}
		if req.Options.ReasoningDetails {
			reasoning.Summary = shared.ReasoningSummaryAuto
		}
		params.Reasoning = reasoning
	}

	if opts.HasKey("text") {
		text, err := decodeJSONValue[responses.ResponseTextConfigParam](opts["text"])
		if err != nil {
			return responses.ResponseNewParams{}, fmt.Errorf("openai text: %w", err)
		}
		params.Text = text
	} else {
		text, hasText, err := buildTextConfig(opts)
		if err != nil {
			return responses.ResponseNewParams{}, err
		}
		if hasText {
			params.Text = text
		}
	}

	if opts.HasKey("input") {
		input, err := decodeJSONValue[responses.ResponseNewParamsInputUnion](opts["input"])
		if err != nil {
			return responses.ResponseNewParams{}, fmt.Errorf("openai input: %w", err)
		}
		params.Input = input
	} else {
		input, err := buildInputFromMessages(req.Messages)
		if err != nil {
			return responses.ResponseNewParams{}, err
		}
		params.Input = input
	}

	rawTools, err := decodeRawTools(opts)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}
	compatTools, err := buildFunctionTools(req.Tools)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}
	if len(rawTools)+len(compatTools) > 0 {
		params.Tools = append(rawTools, compatTools...)
	}

	if opts.HasKey("tool_choice") {
		choice, err := decodeJSONValue[responses.ResponseNewParamsToolChoiceUnion](opts["tool_choice"])
		if err != nil {
			return responses.ResponseNewParams{}, fmt.Errorf("openai tool_choice: %w", err)
		}
		params.ToolChoice = choice
	} else if req.ToolChoice != nil {
		params.ToolChoice, err = buildToolChoice(*req.ToolChoice)
		if err != nil {
			return responses.ResponseNewParams{}, err
		}
	}

	return params, nil
}

func validateOpenAIResponsesOptions(opts structs.JSONMap) error {
	if len(opts) == 0 {
		return nil
	}
	var unsupported []string
	for key := range opts {
		if _, ok := openAIResponsesOptionKeys[key]; !ok {
			unsupported = append(unsupported, key)
		}
	}
	if len(unsupported) == 0 {
		return nil
	}
	sort.Strings(unsupported)
	return fmt.Errorf("openai_resp provider does not support OpenAI options: %s", strings.Join(unsupported, ", "))
}

func validateOpenAIResponsesConflicts(req *chat.Request, opts structs.JSONMap) error {
	if opts.HasKey("input") && len(req.Messages) > 0 {
		return fmt.Errorf("openai_resp provider does not allow openai.input together with chat messages")
	}
	if opts.HasKey("reasoning") && (req.Options.ReasoningEffort != nil || req.Options.ReasoningDetails) {
		return fmt.Errorf("openai_resp provider does not allow openai.reasoning together with WithReasoningEffort or WithReasoningDetails")
	}
	if opts.HasKey("tool_choice") && req.ToolChoice != nil {
		return fmt.Errorf("openai_resp provider does not allow openai.tool_choice together with WithToolChoice")
	}
	if opts.HasKey("previous_response_id") && opts.HasKey("conversation") {
		return fmt.Errorf("openai_resp provider does not allow openai.previous_response_id together with openai.conversation")
	}
	if opts.HasKey("text") && (opts.HasKey("response_format") || opts.HasKey("verbosity")) {
		return fmt.Errorf("openai_resp provider does not allow openai.text together with response_format or verbosity shortcuts")
	}
	if req.Options.User != nil && opts.HasKey("user") {
		return fmt.Errorf("openai_resp provider does not allow openai.user together with WithUser")
	}
	return nil
}

func applyRawRootOptions(params *responses.ResponseNewParams, opts structs.JSONMap) error {
	if params == nil || len(opts) == 0 {
		return nil
	}
	if opts.HasKey("background") {
		params.Background = openai.Bool(opts.GetBool("background"))
	}
	if opts.HasKey("conversation") {
		conversation, err := decodeJSONValue[responses.ResponseNewParamsConversationUnion](opts["conversation"])
		if err != nil {
			return fmt.Errorf("openai conversation: %w", err)
		}
		params.Conversation = conversation
	}
	if opts.HasKey("include") {
		include, err := decodeJSONValue[[]responses.ResponseIncludable](opts["include"])
		if err != nil {
			return fmt.Errorf("openai include: %w", err)
		}
		params.Include = include
	}
	if opts.HasKey("instructions") {
		if val := strings.TrimSpace(opts.GetString("instructions")); val != "" {
			params.Instructions = openai.String(val)
		}
	}
	if opts.HasKey("max_tool_calls") {
		val, ok := oaicompat.ToInt64(opts["max_tool_calls"])
		if !ok {
			return fmt.Errorf("openai max_tool_calls must be numeric")
		}
		params.MaxToolCalls = openai.Int(val)
	}
	if opts.HasKey("metadata") {
		if meta := oaicompat.ParseStringMap(opts["metadata"]); len(meta) > 0 {
			params.Metadata = shared.Metadata(meta)
		}
	}
	if opts.HasKey("parallel_tool_calls") {
		params.ParallelToolCalls = openai.Bool(opts.GetBool("parallel_tool_calls"))
	}
	if opts.HasKey("previous_response_id") {
		if val := strings.TrimSpace(opts.GetString("previous_response_id")); val != "" {
			params.PreviousResponseID = openai.String(val)
		}
	}
	if opts.HasKey("prompt") {
		prompt, err := decodeJSONValue[responses.ResponsePromptParam](opts["prompt"])
		if err != nil {
			return fmt.Errorf("openai prompt: %w", err)
		}
		params.Prompt = prompt
	}
	if opts.HasKey("prompt_cache_key") {
		if val := strings.TrimSpace(opts.GetString("prompt_cache_key")); val != "" {
			params.PromptCacheKey = openai.String(val)
		}
	}
	if opts.HasKey("prompt_cache_retention") {
		if val := strings.TrimSpace(opts.GetString("prompt_cache_retention")); val != "" {
			params.SetExtraFields(map[string]any{
				"prompt_cache_retention": val,
			})
		}
	}
	if opts.HasKey("safety_identifier") {
		if val := strings.TrimSpace(opts.GetString("safety_identifier")); val != "" {
			params.SafetyIdentifier = openai.String(val)
		}
	}
	if opts.HasKey("service_tier") {
		if val := strings.TrimSpace(opts.GetString("service_tier")); val != "" {
			params.ServiceTier = responses.ResponseNewParamsServiceTier(val)
		}
	}
	if opts.HasKey("store") {
		params.Store = openai.Bool(opts.GetBool("store"))
	}
	if opts.HasKey("stream_options") {
		streamOptions, err := decodeJSONValue[responses.ResponseNewParamsStreamOptions](opts["stream_options"])
		if err != nil {
			return fmt.Errorf("openai stream_options: %w", err)
		}
		params.StreamOptions = streamOptions
	}
	if opts.HasKey("top_logprobs") {
		val, ok := oaicompat.ToInt64(opts["top_logprobs"])
		if !ok {
			return fmt.Errorf("openai top_logprobs must be numeric")
		}
		params.TopLogprobs = openai.Int(val)
	}
	if opts.HasKey("truncation") {
		if val := strings.TrimSpace(opts.GetString("truncation")); val != "" {
			params.Truncation = responses.ResponseNewParamsTruncation(val)
		}
	}
	if opts.HasKey("user") {
		if val := strings.TrimSpace(opts.GetString("user")); val != "" {
			params.User = openai.String(val)
		}
	}
	return nil
}

func buildTextConfig(opts structs.JSONMap) (responses.ResponseTextConfigParam, bool, error) {
	cfg := responses.ResponseTextConfigParam{}
	hasText := false

	if opts.HasKey("verbosity") {
		val := strings.TrimSpace(opts.GetString("verbosity"))
		if val != "" {
			cfg.Verbosity = responses.ResponseTextConfigVerbosity(val)
			hasText = true
		}
	}
	if opts.HasKey("response_format") {
		if err := applyResponseFormat(&cfg, opts["response_format"]); err != nil {
			return responses.ResponseTextConfigParam{}, false, err
		}
		hasText = true
	}

	return cfg, hasText, nil
}

func applyResponseFormat(cfg *responses.ResponseTextConfigParam, value any) error {
	switch v := value.(type) {
	case string:
		return setResponseFormatByType(cfg, v, nil)
	case map[string]any:
		return setResponseFormatByType(cfg, "", v)
	case structs.JSONMap:
		return setResponseFormatByType(cfg, "", map[string]any(v))
	default:
		return fmt.Errorf("openai response_format has unsupported type %T", value)
	}
}

func setResponseFormatByType(cfg *responses.ResponseTextConfigParam, typeName string, payload map[string]any) error {
	if cfg == nil {
		return nil
	}
	typ := strings.ToLower(strings.TrimSpace(typeName))
	if typ == "" && payload != nil {
		if raw, ok := payload["type"].(string); ok {
			typ = strings.ToLower(strings.TrimSpace(raw))
		}
	}

	switch typ {
	case "text":
		cfg.Format = responses.ResponseFormatTextConfigUnionParam{
			OfText: &shared.ResponseFormatTextParam{Type: "text"},
		}
		return nil
	case "json_object":
		cfg.Format = responses.ResponseFormatTextConfigUnionParam{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{Type: "json_object"},
		}
		return nil
	case "json_schema":
		schemaPayload := payload
		if payload != nil {
			if raw, ok := payload["json_schema"]; ok {
				switch schema := raw.(type) {
				case map[string]any:
					schemaPayload = schema
				case structs.JSONMap:
					schemaPayload = map[string]any(schema)
				}
			}
		}
		if schemaPayload == nil {
			return fmt.Errorf("openai response_format json_schema requires payload")
		}
		name, _ := schemaPayload["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("openai response_format json_schema requires name")
		}
		out := responses.ResponseFormatTextJSONSchemaConfigParam{
			Name:   name,
			Schema: map[string]any{},
		}
		if schema, ok := schemaPayload["schema"].(map[string]any); ok {
			out.Schema = schema
		} else if schema, ok := schemaPayload["schema"].(structs.JSONMap); ok {
			out.Schema = map[string]any(schema)
		}
		if raw, ok := schemaPayload["strict"].(bool); ok {
			out.Strict = openai.Bool(raw)
		}
		if raw, ok := schemaPayload["description"].(string); ok && strings.TrimSpace(raw) != "" {
			out.Description = openai.String(raw)
		}
		cfg.Format = responses.ResponseFormatTextConfigUnionParam{
			OfJSONSchema: &out,
		}
		return nil
	default:
		return fmt.Errorf("openai response_format type %q is not supported on openai_resp", typ)
	}
}

func decodeRawTools(opts structs.JSONMap) ([]responses.ToolUnionParam, error) {
	if !opts.HasKey("tools") {
		return nil, nil
	}
	tools, err := decodeJSONValue[[]responses.ToolUnionParam](opts["tools"])
	if err != nil {
		return nil, fmt.Errorf("openai tools: %w", err)
	}
	if len(tools) == 0 {
		return nil, nil
	}
	return tools, nil
}

func buildFunctionTools(tools []chat.Tool) ([]responses.ToolUnionParam, error) {
	out := make([]responses.ToolUnionParam, 0, len(tools))
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
			if tool.Function.Strict != nil && *tool.Function.Strict {
				normalizeOpenAIStrictSchema(params)
			}
		}
		fn := responses.FunctionToolParam{
			Name:       name,
			Parameters: params,
			// Responses API treats omitted strict as provider-default strict mode.
			// Default to false here so uniai preserves ordinary JSON Schema semantics
			// unless the caller explicitly opts into strict validation.
			Strict: openai.Bool(false),
		}
		if tool.Function.Strict != nil {
			fn.Strict = openai.Bool(*tool.Function.Strict)
		}
		if desc := strings.TrimSpace(tool.Function.Description); desc != "" {
			fn.Description = openai.String(desc)
		}
		out = append(out, responses.ToolUnionParam{
			OfFunction: &fn,
		})
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func normalizeOpenAIStrictSchema(value any) {
	switch node := value.(type) {
	case map[string]any:
		normalizeOpenAIStrictSchemaMap(node)
	case []any:
		for _, item := range node {
			normalizeOpenAIStrictSchema(item)
		}
	}
}

func normalizeOpenAIStrictSchemaMap(node map[string]any) {
	if isObjectSchema(node) {
		if _, ok := node["additionalProperties"]; !ok {
			node["additionalProperties"] = false
		}
	}

	for _, key := range []string{"properties", "patternProperties", "definitions", "$defs"} {
		if props, ok := node[key].(map[string]any); ok {
			for _, val := range props {
				normalizeOpenAIStrictSchema(val)
			}
		}
	}
	for _, key := range []string{"items", "additionalProperties", "contains", "not", "if", "then", "else", "propertyNames"} {
		if val, ok := node[key]; ok {
			normalizeOpenAIStrictSchema(val)
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		if items, ok := node[key].([]any); ok {
			for _, val := range items {
				normalizeOpenAIStrictSchema(val)
			}
		}
	}
}

func isObjectSchema(node map[string]any) bool {
	switch t := node["type"].(type) {
	case string:
		return strings.EqualFold(strings.TrimSpace(t), "object")
	case []string:
		for _, item := range t {
			if strings.EqualFold(strings.TrimSpace(item), "object") {
				return true
			}
		}
	case []any:
		for _, item := range t {
			if s, ok := item.(string); ok && strings.EqualFold(strings.TrimSpace(s), "object") {
				return true
			}
		}
	}
	_, hasProps := node["properties"]
	return hasProps
}

func buildToolChoice(choice chat.ToolChoice) (responses.ResponseNewParamsToolChoiceUnion, error) {
	switch strings.ToLower(strings.TrimSpace(choice.Mode)) {
	case "", "auto":
		return responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: openai.Opt(responses.ToolChoiceOptionsAuto),
		}, nil
	case "none":
		return responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: openai.Opt(responses.ToolChoiceOptionsNone),
		}, nil
	case "required":
		return responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: openai.Opt(responses.ToolChoiceOptionsRequired),
		}, nil
	case "function":
		name := strings.TrimSpace(choice.FunctionName)
		if name == "" {
			return responses.ResponseNewParamsToolChoiceUnion{}, fmt.Errorf("tool_choice function_name is required when mode=function")
		}
		return responses.ResponseNewParamsToolChoiceUnion{
			OfFunctionTool: &responses.ToolChoiceFunctionParam{Name: name},
		}, nil
	default:
		return responses.ResponseNewParamsToolChoiceUnion{}, fmt.Errorf("unsupported tool choice mode %q", choice.Mode)
	}
}

func buildInputFromMessages(messages []chat.Message) (responses.ResponseNewParamsInputUnion, error) {
	items := make([]responses.ResponseInputItemUnionParam, 0, len(messages))
	autoID := 0

	for _, msg := range messages {
		if strings.TrimSpace(msg.Name) != "" {
			return responses.ResponseNewParamsInputUnion{}, fmt.Errorf("openai_resp provider does not support message names")
		}
		if msg.Role != chat.RoleAssistant && len(msg.ToolCalls) > 0 {
			return responses.ResponseNewParamsInputUnion{}, fmt.Errorf("role %q does not support tool calls", msg.Role)
		}

		switch msg.Role {
		case chat.RoleSystem:
			text, err := chat.MessageText(msg)
			if err != nil {
				return responses.ResponseNewParamsInputUnion{}, fmt.Errorf("role %q: %w", msg.Role, err)
			}
			if strings.TrimSpace(text) != "" {
				items = append(items, responses.ResponseInputItemParamOfMessage(text, responses.EasyInputMessageRoleSystem))
			}
		case chat.RoleUser:
			content, ok, err := buildUserInputContent(msg)
			if err != nil {
				return responses.ResponseNewParamsInputUnion{}, fmt.Errorf("role %q: %w", msg.Role, err)
			}
			if !ok {
				continue
			}
			items = append(items, responses.ResponseInputItemParamOfMessage(content, responses.EasyInputMessageRoleUser))
		case chat.RoleAssistant:
			text, err := chat.MessageText(chat.Message{Role: msg.Role, Content: msg.Content, Parts: msg.Parts})
			if err != nil {
				return responses.ResponseNewParamsInputUnion{}, fmt.Errorf("role %q: %w", msg.Role, err)
			}
			if strings.TrimSpace(text) != "" {
				items = append(items, responses.ResponseInputItemParamOfMessage(text, responses.EasyInputMessageRoleAssistant))
			}
			for _, call := range msg.ToolCalls {
				name := strings.TrimSpace(call.Function.Name)
				if name == "" {
					return responses.ResponseNewParamsInputUnion{}, fmt.Errorf("assistant tool call name is required")
				}
				callType := strings.ToLower(strings.TrimSpace(call.Type))
				if callType == "" {
					callType = "function"
				}
				if callType != "function" {
					return responses.ResponseNewParamsInputUnion{}, fmt.Errorf("unsupported assistant tool call type %q", call.Type)
				}
				callID := strings.TrimSpace(call.ID)
				if callID == "" {
					autoID++
					callID = fmt.Sprintf("call_%d", autoID)
				}
				args := strings.TrimSpace(call.Function.Arguments)
				if args == "" {
					args = "{}"
				}
				items = append(items, responses.ResponseInputItemParamOfFunctionCall(args, callID, name))
			}
		case chat.RoleTool:
			callID := strings.TrimSpace(msg.ToolCallID)
			if callID == "" {
				return responses.ResponseNewParamsInputUnion{}, fmt.Errorf("tool_call_id is required for tool messages")
			}
			text, err := chat.MessageText(msg)
			if err != nil {
				return responses.ResponseNewParamsInputUnion{}, fmt.Errorf("role %q: %w", msg.Role, err)
			}
			items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(callID, text))
		default:
			return responses.ResponseNewParamsInputUnion{}, fmt.Errorf("unsupported message role %q", msg.Role)
		}
	}

	if len(items) == 0 {
		return responses.ResponseNewParamsInputUnion{}, fmt.Errorf("messages are required")
	}

	return responses.ResponseNewParamsInputUnion{
		OfInputItemList: items,
	}, nil
}

func buildUserInputContent(msg chat.Message) (responses.ResponseInputMessageContentListParam, bool, error) {
	parts := chat.NormalizeMessageParts(msg)
	if len(parts) == 0 {
		return nil, false, nil
	}

	out := make(responses.ResponseInputMessageContentListParam, 0, len(parts))
	for _, part := range parts {
		if err := chat.ValidatePart(part); err != nil {
			return nil, false, err
		}
		switch part.Type {
		case chat.PartTypeText:
			out = append(out, responses.ResponseInputContentUnionParam{
				OfInputText: &responses.ResponseInputTextParam{Text: part.Text},
			})
		case chat.PartTypeImageURL:
			out = append(out, responses.ResponseInputContentUnionParam{
				OfInputImage: &responses.ResponseInputImageParam{
					Detail:   responses.ResponseInputImageDetailAuto,
					ImageURL: openai.String(part.URL),
				},
			})
		case chat.PartTypeImageBase64:
			mimeType := strings.TrimSpace(part.MIMEType)
			if mimeType == "" {
				return nil, false, fmt.Errorf("part type %q requires mime_type for openai_resp", chat.PartTypeImageBase64)
			}
			out = append(out, responses.ResponseInputContentUnionParam{
				OfInputImage: &responses.ResponseInputImageParam{
					Detail:   responses.ResponseInputImageDetailAuto,
					ImageURL: openai.String(fmt.Sprintf("data:%s;base64,%s", mimeType, part.DataBase64)),
				},
			})
		default:
			return nil, false, fmt.Errorf("unsupported part type %q", part.Type)
		}
	}

	return out, true, nil
}

func toResult(resp *responses.Response) *chat.Result {
	if resp == nil {
		return &chat.Result{Warnings: []string{"openai responses response is nil"}}
	}

	usage := chat.Usage{
		InputTokens:  int(resp.Usage.InputTokens),
		OutputTokens: int(resp.Usage.OutputTokens),
		TotalTokens:  int(resp.Usage.TotalTokens),
	}
	if cached := int(resp.Usage.InputTokensDetails.CachedTokens); cached > 0 {
		usage.Cache.CachedInputTokens = cached
	}

	result := &chat.Result{
		ID:    resp.ID,
		Text:  resp.OutputText(),
		Model: string(resp.Model),
		Usage: usage,
		Raw:   resp,
	}

	var textMessages []chat.Message
	var toolCalls []chat.ToolCall
	var reasoning *chat.ReasoningResult
	for _, item := range resp.Output {
		switch out := item.AsAny().(type) {
		case responses.ResponseOutputMessage:
			text, parts := extractOutputMessage(out)
			if len(parts) > 0 {
				result.Parts = append(result.Parts, parts...)
			}
			if strings.TrimSpace(text) != "" {
				textMessages = append(textMessages, chat.Message{
					Role:    chat.RoleAssistant,
					Content: text,
					Parts:   parts,
				})
			}
		case responses.ResponseFunctionToolCall:
			toolCalls = append(toolCalls, chat.ToolCall{
				ID:   out.CallID,
				Type: "function",
				Function: chat.ToolCallFunction{
					Name:      out.Name,
					Arguments: out.Arguments,
				},
			})
		case responses.ResponseReasoningItem:
			if reasoning == nil {
				reasoning = &chat.ReasoningResult{}
			}
			for _, summary := range out.Summary {
				if strings.TrimSpace(summary.Text) != "" {
					reasoning.Summary = append(reasoning.Summary, summary.Text)
				}
			}
			for _, content := range out.Content {
				if strings.TrimSpace(content.Text) != "" {
					reasoning.Blocks = append(reasoning.Blocks, chat.ReasoningBlock{
						Type: "thinking",
						Text: content.Text,
					})
				}
			}
			if strings.TrimSpace(out.EncryptedContent) != "" {
				reasoning.Blocks = append(reasoning.Blocks, chat.ReasoningBlock{
					Type: "encrypted",
					Data: out.EncryptedContent,
				})
			}
		}
	}

	if len(toolCalls) > 0 {
		result.ToolCalls = toolCalls
		textMessages = append(textMessages, chat.Message{
			Role:      chat.RoleAssistant,
			ToolCalls: append([]chat.ToolCall{}, toolCalls...),
		})
	}
	if len(textMessages) > 0 {
		result.Messages = textMessages
	}
	if reasoning != nil && (len(reasoning.Summary) > 0 || len(reasoning.Blocks) > 0) {
		result.Reasoning = reasoning
	}
	chat.EnsureResultParts(result)
	return result
}

func extractOutputMessage(msg responses.ResponseOutputMessage) (string, []chat.Part) {
	var text strings.Builder
	parts := make([]chat.Part, 0, len(msg.Content))
	for _, content := range msg.Content {
		switch item := content.AsAny().(type) {
		case responses.ResponseOutputText:
			if item.Text == "" {
				continue
			}
			text.WriteString(item.Text)
			parts = append(parts, chat.TextPart(item.Text))
		}
	}
	return text.String(), parts
}

func responseStatusError(resp *responses.Response) error {
	if resp == nil {
		return fmt.Errorf("openai responses response is nil")
	}
	switch resp.Status {
	case responses.ResponseStatusFailed:
		if strings.TrimSpace(resp.Error.Message) != "" {
			return fmt.Errorf("openai responses failed: %s", resp.Error.Message)
		}
		return fmt.Errorf("openai responses failed")
	case responses.ResponseStatusIncomplete:
		reason := strings.TrimSpace(resp.IncompleteDetails.Reason)
		if reason != "" {
			return fmt.Errorf("openai responses incomplete: %s", reason)
		}
		return fmt.Errorf("openai responses incomplete")
	default:
		return nil
	}
}

type streamToolCallState struct {
	CallID string
	ItemID string
	Name   string
}

type responseStreamState struct {
	toolCalls map[int]streamToolCallState
	completed *responses.Response
}

func streamChat(
	ctx context.Context,
	client *openai.Client,
	params responses.ResponseNewParams,
	onStream chat.OnStreamFunc,
) (*chat.Result, error) {
	stream := client.Responses.NewStreaming(ctx, params)
	state := &responseStreamState{
		toolCalls: map[int]streamToolCallState{},
	}

	for stream.Next() {
		ev := stream.Current()
		if err := processStreamEvent(ev, state, onStream); err != nil {
			stream.Close()
			return nil, err
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	if state.completed == nil {
		return nil, fmt.Errorf("openai responses stream ended without a completed response")
	}
	if err := responseStatusError(state.completed); err != nil {
		return nil, err
	}

	result := toResult(state.completed)
	if err := onStream(chat.StreamEvent{
		Done:  true,
		Usage: &result.Usage,
	}); err != nil {
		return nil, err
	}
	return result, nil
}

func processStreamEvent(ev responses.ResponseStreamEventUnion, state *responseStreamState, onStream chat.OnStreamFunc) error {
	switch event := ev.AsAny().(type) {
	case responses.ResponseOutputItemAddedEvent:
		registerStreamOutputItem(event.Item, int(event.OutputIndex), state)
	case responses.ResponseOutputItemDoneEvent:
		registerStreamOutputItem(event.Item, int(event.OutputIndex), state)
	case responses.ResponseTextDeltaEvent:
		if event.Delta == "" {
			return nil
		}
		return onStream(chat.StreamEvent{Delta: event.Delta})
	case responses.ResponseFunctionCallArgumentsDeltaEvent:
		meta := state.toolCalls[int(event.OutputIndex)]
		id := strings.TrimSpace(meta.CallID)
		if id == "" {
			id = strings.TrimSpace(meta.ItemID)
		}
		if id == "" {
			id = strings.TrimSpace(event.ItemID)
		}
		return onStream(chat.StreamEvent{
			ToolCallDelta: &chat.ToolCallDelta{
				Index:     int(event.OutputIndex),
				ID:        id,
				Name:      meta.Name,
				ArgsChunk: event.Delta,
			},
		})
	case responses.ResponseFunctionCallArgumentsDoneEvent:
		meta := state.toolCalls[int(event.OutputIndex)]
		meta.Name = firstNonEmptyString(strings.TrimSpace(event.Name), meta.Name)
		if meta.ItemID == "" {
			meta.ItemID = strings.TrimSpace(event.ItemID)
		}
		state.toolCalls[int(event.OutputIndex)] = meta
		id := strings.TrimSpace(meta.CallID)
		if id == "" {
			id = firstNonEmptyString(meta.ItemID, strings.TrimSpace(event.ItemID))
		}
		return onStream(chat.StreamEvent{
			ToolCallDelta: &chat.ToolCallDelta{
				Index: int(event.OutputIndex),
				ID:    id,
				Name:  meta.Name,
			},
		})
	case responses.ResponseCompletedEvent:
		state.completed = &event.Response
	case responses.ResponseIncompleteEvent:
		state.completed = &event.Response
	case responses.ResponseFailedEvent:
		state.completed = &event.Response
	}
	return nil
}

func registerStreamOutputItem(item responses.ResponseOutputItemUnion, outputIndex int, state *responseStreamState) {
	if state == nil {
		return
	}
	call, ok := item.AsAny().(responses.ResponseFunctionToolCall)
	if !ok {
		return
	}
	state.toolCalls[outputIndex] = streamToolCallState{
		CallID: strings.TrimSpace(call.CallID),
		ItemID: strings.TrimSpace(call.ID),
		Name:   strings.TrimSpace(call.Name),
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

func decodeJSONValue[T any](value any) (T, error) {
	var out T
	data, err := json.Marshal(value)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}
