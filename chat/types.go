package chat

import (
	"fmt"

	"github.com/lyricat/goutils/structs"
)

const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function ToolCallFunction `json:"function,omitempty"`
}

type ToolCallFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
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
	OpenAI             structs.JSONMap    `json:"openai_options,omitempty"`
	Azure              structs.JSONMap    `json:"azure_options,omitempty"`
	Anthropic          structs.JSONMap    `json:"anthropic_options,omitempty"`
	Bedrock            structs.JSONMap    `json:"bedrock_options,omitempty"`
	Susanoo            structs.JSONMap    `json:"susanoo_options,omitempty"`
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

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type Result struct {
	Text      string     `json:"text,omitempty"`
	Model     string     `json:"model,omitempty"`
	Messages  []Message  `json:"messages,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Usage     Usage      `json:"usage,omitempty"`
	Raw       any        `json:"raw,omitempty"`
	Warnings  []string   `json:"warnings,omitempty"`
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
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("messages are required")
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

func WithSusanooOptions(opts structs.JSONMap) Option {
	return func(r *Request) { r.Options.Susanoo = opts }
}

func WithCloudflareOptions(opts structs.JSONMap) Option {
	return func(r *Request) { r.Options.Cloudflare = opts }
}

func WithTools(tools []Tool) Option {
	return func(r *Request) { r.Tools = append([]Tool{}, tools...) }
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

func Assistant(text string) Message {
	return Message{Role: RoleAssistant, Content: text}
}

func ToolResult(toolCallID, content string) Message {
	return Message{Role: RoleTool, Content: content, ToolCallID: toolCallID}
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
