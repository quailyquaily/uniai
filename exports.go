package uniai

import (
	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/audio"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/classify"
	"github.com/quailyquaily/uniai/embedding"
	"github.com/quailyquaily/uniai/image"
	"github.com/quailyquaily/uniai/rerank"
)

// Chat re-exports
type (
	ChatOption         = chat.Option
	ChatRequest        = chat.Request
	ChatResult         = chat.Result
	ChatOptions        = chat.Options
	Message            = chat.Message
	Part               = chat.Part
	Tool               = chat.Tool
	ToolFunction       = chat.ToolFunction
	ToolChoice         = chat.ToolChoice
	ToolCall           = chat.ToolCall
	ToolCallFunction   = chat.ToolCallFunction
	DebugFn            = chat.DebugFn
	ToolsEmulationMode = chat.ToolsEmulationMode
	OnStreamFunc       = chat.OnStreamFunc
	StreamEvent        = chat.StreamEvent
	ToolCallDelta      = chat.ToolCallDelta
)

const (
	RoleSystem    = chat.RoleSystem
	RoleUser      = chat.RoleUser
	RoleAssistant = chat.RoleAssistant
	RoleTool      = chat.RoleTool
)

const (
	PartTypeText        = chat.PartTypeText
	PartTypeImageURL    = chat.PartTypeImageURL
	PartTypeImageBase64 = chat.PartTypeImageBase64
)

const (
	ToolsEmulationOff      = chat.ToolsEmulationOff
	ToolsEmulationFallback = chat.ToolsEmulationFallback
	ToolsEmulationForce    = chat.ToolsEmulationForce
)

func WithModel(model string) ChatOption              { return chat.WithModel(model) }
func WithProvider(provider string) ChatOption        { return chat.WithProvider(provider) }
func WithMessages(msgs ...Message) ChatOption        { return chat.WithMessages(msgs...) }
func WithMessage(msg Message) ChatOption             { return chat.WithMessage(msg) }
func WithReplaceMessages(msgs ...Message) ChatOption { return chat.WithReplaceMessages(msgs...) }
func WithTemperature(v float64) ChatOption           { return chat.WithTemperature(v) }
func WithTopP(v float64) ChatOption                  { return chat.WithTopP(v) }
func WithMaxTokens(v int) ChatOption                 { return chat.WithMaxTokens(v) }
func WithStop(stop string) ChatOption                { return chat.WithStop(stop) }
func WithStopWords(stops ...string) ChatOption       { return chat.WithStopWords(stops...) }
func WithPresencePenalty(v float64) ChatOption       { return chat.WithPresencePenalty(v) }
func WithFrequencyPenalty(v float64) ChatOption      { return chat.WithFrequencyPenalty(v) }
func WithUser(user string) ChatOption                { return chat.WithUser(user) }
func WithToolsEmulationMode(mode ToolsEmulationMode) ChatOption {
	return chat.WithToolsEmulationMode(mode)
}
func WithOnStream(fn OnStreamFunc) ChatOption { return chat.WithOnStream(fn) }
func WithDebugFn(fn DebugFn) ChatOption       { return chat.WithDebugFn(fn) }
func WithOpenAIOptions(opts structs.JSONMap) ChatOption {
	return chat.WithOpenAIOptions(opts)
}
func WithAzureOptions(opts structs.JSONMap) ChatOption {
	return chat.WithAzureOptions(opts)
}
func WithAnthropicOptions(opts structs.JSONMap) ChatOption {
	return chat.WithAnthropicOptions(opts)
}
func WithBedrockOptions(opts structs.JSONMap) ChatOption {
	return chat.WithBedrockOptions(opts)
}
func WithCloudflareOptions(opts structs.JSONMap) ChatOption {
	return chat.WithCloudflareOptions(opts)
}
func WithTools(tools []Tool) ChatOption           { return chat.WithTools(tools) }
func WithToolChoice(choice ToolChoice) ChatOption { return chat.WithToolChoice(choice) }

func System(text string) Message                    { return chat.System(text) }
func User(text string) Message                      { return chat.User(text) }
func Assistant(text string) Message                 { return chat.Assistant(text) }
func ToolResult(toolCallID, content string) Message { return chat.ToolResult(toolCallID, content) }
func SystemParts(parts ...Part) Message             { return chat.SystemParts(parts...) }
func UserParts(parts ...Part) Message               { return chat.UserParts(parts...) }
func AssistantParts(parts ...Part) Message          { return chat.AssistantParts(parts...) }
func TextPart(text string) Part                     { return chat.TextPart(text) }
func ImageURLPart(url string) Part                  { return chat.ImageURLPart(url) }
func ImageBase64Part(mimeType, dataBase64 string) Part {
	return chat.ImageBase64Part(mimeType, dataBase64)
}

func ToolChoiceAuto() ToolChoice                { return chat.ToolChoiceAuto() }
func ToolChoiceNone() ToolChoice                { return chat.ToolChoiceNone() }
func ToolChoiceRequired() ToolChoice            { return chat.ToolChoiceRequired() }
func ToolChoiceFunction(name string) ToolChoice { return chat.ToolChoiceFunction(name) }

func FunctionTool(name, description string, paramsJSON []byte) Tool {
	return chat.FunctionTool(name, description, paramsJSON)
}

// Embedding re-exports
type (
	EmbeddingOption  = embedding.Option
	EmbeddingRequest = embedding.Request
	EmbeddingInput   = embedding.Input
	EmbeddingResult  = embedding.Result
)

func Embedding(model string, texts ...string) EmbeddingOption {
	return embedding.Embedding(model, texts...)
}
func WithEmbeddingProvider(provider string) EmbeddingOption { return embedding.WithProvider(provider) }
func WithEmbeddingInputs(inputs ...EmbeddingInput) EmbeddingOption {
	return embedding.WithInputs(inputs...)
}
func WithEmbeddingOptions(opts embedding.Options) EmbeddingOption { return embedding.WithOptions(opts) }

// Image re-exports
type (
	ImageOption  = image.Option
	ImageRequest = image.Request
	ImageResult  = image.Result
)

func Image(model, prompt string) ImageOption          { return image.Image(model, prompt) }
func WithImageProvider(provider string) ImageOption   { return image.WithProvider(provider) }
func WithCount(count int) ImageOption                 { return image.WithCount(count) }
func WithImageOptions(opts image.Options) ImageOption { return image.WithOptions(opts) }

// Audio re-exports
type (
	AudioOption  = audio.Option
	AudioRequest = audio.Request
	AudioResult  = audio.Result
	AudioSegment = audio.Segment
)

func Audio(model, audioBase64 string) AudioOption     { return audio.Audio(model, audioBase64) }
func WithAudioProvider(provider string) AudioOption   { return audio.WithProvider(provider) }
func WithAudio(audioBase64 string) AudioOption        { return audio.WithAudio(audioBase64) }
func WithAudioOptions(opts audio.Options) AudioOption { return audio.WithOptions(opts) }

// Rerank re-exports
type (
	RerankOption = rerank.Option
	RerankResult = rerank.Result
	RerankInput  = rerank.Input
)

func Rerank(model, query string, docs ...RerankInput) RerankOption {
	return rerank.Rerank(model, query, docs...)
}
func WithRerankProvider(provider string) RerankOption  { return rerank.WithProvider(provider) }
func WithTopN(topN int) RerankOption                   { return rerank.WithTopN(topN) }
func WithReturnDocuments(returnDocs bool) RerankOption { return rerank.WithReturnDocuments(returnDocs) }

// Classify re-exports
type (
	ClassifyOption = classify.Option
	ClassifyResult = classify.Result
	ClassifyInput  = classify.Input
)

func Classify(model string, labels []string, inputs ...ClassifyInput) ClassifyOption {
	return classify.Classify(model, labels, inputs...)
}
func WithClassifyProvider(provider string) ClassifyOption { return classify.WithProvider(provider) }
