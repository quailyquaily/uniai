package chat

import (
	"encoding/json"
	"fmt"

	"github.com/lyricat/goutils/structs"
)

const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

const (
	PartTypeText        = "text"
	PartTypeImageURL    = "image_url"
	PartTypeImageBase64 = "image_base64"
)

type Part struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	URL          string        `json:"url,omitempty"`
	DataBase64   string        `json:"data_base64,omitempty"`
	MIMEType     string        `json:"mime_type,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Parts      []Part     `json:"parts,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID               string           `json:"id,omitempty"`
	Type             string           `json:"type,omitempty"`
	Function         ToolCallFunction `json:"function,omitempty"`
	ThoughtSignature string           `json:"thought_signature,omitempty"`
}

type ToolCallFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type Tool struct {
	Type         string        `json:"type"`
	Function     ToolFunction  `json:"function"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type ToolFunction struct {
	Name                 string `json:"name"`
	Description          string `json:"description,omitempty"`
	ParametersJSONSchema []byte `json:"parameters,omitempty"`
	Strict               *bool  `json:"strict,omitempty"`
}

type ToolChoice struct {
	Mode         string `json:"mode,omitempty"` // auto|none|required|function
	FunctionName string `json:"function_name,omitempty"`
}

type CacheControl struct {
	TTL string `json:"ttl,omitempty"`
}

type ReasoningEffort string

const (
	ReasoningEffortNone    ReasoningEffort = "none"
	ReasoningEffortMinimal ReasoningEffort = "minimal"
	ReasoningEffortLow     ReasoningEffort = "low"
	ReasoningEffortMedium  ReasoningEffort = "medium"
	ReasoningEffortHigh    ReasoningEffort = "high"
	ReasoningEffortMax     ReasoningEffort = "max"
	ReasoningEffortXHigh   ReasoningEffort = "xhigh"
)

type DebugFn func(label string, payload string)

type ToolsEmulationMode string

const (
	ToolsEmulationOff      ToolsEmulationMode = "off"
	ToolsEmulationFallback ToolsEmulationMode = "fallback"
	ToolsEmulationForce    ToolsEmulationMode = "force"
)

func ToolChoiceAuto() ToolChoice     { return ToolChoice{Mode: "auto"} }
func ToolChoiceNone() ToolChoice     { return ToolChoice{Mode: "none"} }
func ToolChoiceRequired() ToolChoice { return ToolChoice{Mode: "required"} }
func ToolChoiceFunction(name string) ToolChoice {
	return ToolChoice{Mode: "function", FunctionName: name}
}

type Options struct {
	Temperature        *float64           `json:"temperature,omitempty"`
	TopP               *float64           `json:"top_p,omitempty"`
	MaxTokens          *int               `json:"max_tokens,omitempty"`
	Stop               []string           `json:"stop,omitempty"`
	PresencePenalty    *float64           `json:"presence_penalty,omitempty"`
	FrequencyPenalty   *float64           `json:"frequency_penalty,omitempty"`
	User               *string            `json:"user,omitempty"`
	ReasoningEffort    *ReasoningEffort   `json:"reasoning_effort,omitempty"`
	ReasoningBudget    *int               `json:"reasoning_budget_tokens,omitempty"`
	ReasoningDetails   bool               `json:"reasoning_details,omitempty"`
	OpenAI             structs.JSONMap    `json:"openai_options,omitempty"`
	Azure              structs.JSONMap    `json:"azure_options,omitempty"`
	Anthropic          structs.JSONMap    `json:"anthropic_options,omitempty"`
	Bedrock            structs.JSONMap    `json:"bedrock_options,omitempty"`
	Cloudflare         structs.JSONMap    `json:"cloudflare_options,omitempty"`
	ToolsEmulationMode ToolsEmulationMode `json:"tools_emulation_mode,omitempty"`
	OnStream           OnStreamFunc       `json:"-"`
	DebugFn            DebugFn            `json:"-"`
}

type Request struct {
	Provider   string      `json:"provider,omitempty"`
	Model      string      `json:"model,omitempty"`
	Messages   []Message   `json:"messages"`
	Options    Options     `json:"options,omitempty"`
	Tools      []Tool      `json:"tools,omitempty"`
	ToolChoice *ToolChoice `json:"tool_choice,omitempty"`
}

// Usage reports token usage returned by the provider.
//
// Cache is an additional breakdown. It does not replace or redefine the top-level
// input/output/total token counts.
type Usage struct {
	// InputTokens is the provider-reported total input token count.
	InputTokens int `json:"input_tokens"`

	// OutputTokens is the provider-reported total output token count.
	OutputTokens int `json:"output_tokens"`

	// TotalTokens is the provider-reported total token count when available, or the
	// sum of input and output tokens when uniai derives it.
	TotalTokens int `json:"total_tokens"`

	// Cache contains cache-hit and cache-write breakdown data when the provider
	// returns it.
	Cache UsageCache `json:"cache,omitempty"`
}

// UsageCache contains cache-related token breakdowns.
type UsageCache struct {
	// CachedInputTokens is the number of input tokens read from cache.
	//
	// This is additional breakdown data and should not be subtracted from
	// Usage.InputTokens.
	CachedInputTokens int `json:"cached_input_tokens,omitempty"`

	// CacheCreationInputTokens is the number of input tokens written into cache for
	// future reuse.
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`

	// Details stores provider-specific cache breakdowns, such as TTL bucket counts.
	Details map[string]int `json:"details,omitempty"`
}

func (u Usage) MarshalJSON() ([]byte, error) {
	type usageJSON struct {
		InputTokens  int         `json:"input_tokens"`
		OutputTokens int         `json:"output_tokens"`
		TotalTokens  int         `json:"total_tokens"`
		Cache        *UsageCache `json:"cache,omitempty"`
	}

	var cache *UsageCache
	if !u.Cache.isEmpty() {
		cache = &u.Cache
	}

	return json.Marshal(usageJSON{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		TotalTokens:  u.TotalTokens,
		Cache:        cache,
	})
}

func (u UsageCache) isEmpty() bool {
	return u.CachedInputTokens == 0 && u.CacheCreationInputTokens == 0 && len(u.Details) == 0
}

type Result struct {
	ID        string           `json:"id,omitempty"`
	Text      string           `json:"text,omitempty"`
	Parts     []Part           `json:"parts,omitempty"`
	Model     string           `json:"model,omitempty"`
	Messages  []Message        `json:"messages,omitempty"`
	ToolCalls []ToolCall       `json:"tool_calls,omitempty"`
	Reasoning *ReasoningResult `json:"reasoning,omitempty"`
	Usage     Usage            `json:"usage,omitempty"`
	Raw       any              `json:"raw,omitempty"`
	Warnings  []string         `json:"warnings,omitempty"`
}

type ReasoningResult struct {
	Summary []string         `json:"summary,omitempty"`
	Blocks  []ReasoningBlock `json:"blocks,omitempty"`
}

type ReasoningBlock struct {
	Type      string `json:"type,omitempty"`
	Text      string `json:"text,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"`
}

// OnStreamFunc is called for each streaming event.
// Returning a non-nil error cancels the stream.
type OnStreamFunc func(event StreamEvent) error

// StreamEvent represents a single streaming event from an LLM provider.
type StreamEvent struct {
	Delta         string
	ToolCallDelta *ToolCallDelta
	Usage         *Usage
	Done          bool
}

// ToolCallDelta represents an incremental update to a tool call during streaming.
type ToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	ArgsChunk string
}

type Option func(*Request)

func BuildRequest(opts ...Option) (*Request, error) {
	req := &Request{}
	for _, opt := range opts {
		if opt != nil {
			opt(req)
		}
	}
	if len(req.Messages) == 0 && !req.Options.OpenAI.HasKey("input") {
		return nil, fmt.Errorf("messages are required")
	}
	for i := range req.Messages {
		msg := req.Messages[i]
		if err := ValidateMessageParts(msg); err != nil {
			return nil, fmt.Errorf("message[%d]: %w", i, err)
		}
		for _, part := range msg.Parts {
			if part.Type == PartTypeText {
				continue
			}
			if msg.Role != RoleUser {
				return nil, fmt.Errorf("message[%d]: role %q supports only %q part type", i, msg.Role, PartTypeText)
			}
		}
	}
	for i := range req.Tools {
		if err := ValidateCacheControl(req.Tools[i].CacheControl); err != nil {
			return nil, fmt.Errorf("tool[%d]: %w", i, err)
		}
	}
	return req, nil
}

func WithModel(model string) Option {
	return func(r *Request) { r.Model = model }
}

func WithProvider(provider string) Option {
	return func(r *Request) { r.Provider = provider }
}

func WithMessages(msgs ...Message) Option {
	return func(r *Request) { r.Messages = append(r.Messages, msgs...) }
}

func WithMessage(msg Message) Option {
	return func(r *Request) { r.Messages = append(r.Messages, msg) }
}

func WithReplaceMessages(msgs ...Message) Option {
	return func(r *Request) { r.Messages = append([]Message{}, msgs...) }
}

func WithTemperature(v float64) Option {
	return func(r *Request) { r.Options.Temperature = &v }
}

func WithTopP(v float64) Option {
	return func(r *Request) { r.Options.TopP = &v }
}

func WithMaxTokens(v int) Option {
	return func(r *Request) { r.Options.MaxTokens = &v }
}

func WithStop(stop string) Option {
	return func(r *Request) { r.Options.Stop = []string{stop} }
}

func WithStopWords(stops ...string) Option {
	return func(r *Request) { r.Options.Stop = append([]string{}, stops...) }
}

func WithPresencePenalty(v float64) Option {
	return func(r *Request) { r.Options.PresencePenalty = &v }
}

func WithFrequencyPenalty(v float64) Option {
	return func(r *Request) { r.Options.FrequencyPenalty = &v }
}

func WithUser(user string) Option {
	return func(r *Request) { r.Options.User = &user }
}

func WithReasoningEffort(v ReasoningEffort) Option {
	return func(r *Request) { r.Options.ReasoningEffort = &v }
}

func WithReasoningBudgetTokens(v int) Option {
	return func(r *Request) { r.Options.ReasoningBudget = &v }
}

func WithReasoningDetails() Option {
	return func(r *Request) { r.Options.ReasoningDetails = true }
}

func WithToolsEmulationMode(mode ToolsEmulationMode) Option {
	return func(r *Request) { r.Options.ToolsEmulationMode = mode }
}

func WithOnStream(fn OnStreamFunc) Option {
	return func(r *Request) { r.Options.OnStream = fn }
}

func WithDebugFn(fn DebugFn) Option {
	return func(r *Request) { r.Options.DebugFn = fn }
}

func WithOpenAIOptions(opts structs.JSONMap) Option {
	return func(r *Request) { r.Options.OpenAI = opts }
}

func WithAzureOptions(opts structs.JSONMap) Option {
	return func(r *Request) { r.Options.Azure = opts }
}

func WithAnthropicOptions(opts structs.JSONMap) Option {
	return func(r *Request) { r.Options.Anthropic = opts }
}

func WithBedrockOptions(opts structs.JSONMap) Option {
	return func(r *Request) { r.Options.Bedrock = opts }
}

func WithCloudflareOptions(opts structs.JSONMap) Option {
	return func(r *Request) { r.Options.Cloudflare = opts }
}

func WithTools(tools []Tool) Option {
	return func(r *Request) { r.Tools = CloneTools(tools) }
}

func WithToolChoice(choice ToolChoice) Option {
	return func(r *Request) { r.ToolChoice = &choice }
}

func System(text string) Message {
	return Message{Role: RoleSystem, Content: text}
}

func User(text string) Message {
	return Message{Role: RoleUser, Content: text}
}

func UserParts(parts ...Part) Message {
	return Message{Role: RoleUser, Parts: CloneParts(parts)}
}

func Assistant(text string) Message {
	return Message{Role: RoleAssistant, Content: text}
}

func AssistantParts(parts ...Part) Message {
	return Message{Role: RoleAssistant, Parts: CloneParts(parts)}
}

func SystemParts(parts ...Part) Message {
	return Message{Role: RoleSystem, Parts: CloneParts(parts)}
}

func ToolResult(toolCallID, content string) Message {
	return Message{Role: RoleTool, Content: content, ToolCallID: toolCallID}
}

func TextPart(text string) Part {
	return Part{Type: PartTypeText, Text: text}
}

func ImageURLPart(url string) Part {
	return Part{Type: PartTypeImageURL, URL: url}
}

func ImageBase64Part(mimeType, dataBase64 string) Part {
	return Part{Type: PartTypeImageBase64, MIMEType: mimeType, DataBase64: dataBase64}
}

func WithPartCacheControl(part Part, ctrl CacheControl) Part {
	part.CacheControl = CloneCacheControl(&ctrl)
	return part
}

func WithToolCacheControl(tool Tool, ctrl CacheControl) Tool {
	tool.CacheControl = CloneCacheControl(&ctrl)
	return tool
}

func CacheTTL5m() CacheControl {
	return CacheControl{TTL: "5m"}
}

func CacheTTL1h() CacheControl {
	return CacheControl{TTL: "1h"}
}

func FunctionTool(name, description string, paramsJSON []byte) Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:                 name,
			Description:          description,
			ParametersJSONSchema: paramsJSON,
		},
	}
}
