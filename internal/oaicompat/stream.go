package oaicompat

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/quailyquaily/uniai/chat"
)

// ChatStream performs a streaming chat completion using the OpenAI SDK.
// It invokes onStream for each chunk, accumulates the result, and returns
// the final chat.Result.
func ChatStream(
	ctx context.Context,
	client *openai.Client,
	params openai.ChatCompletionNewParams,
	onStream chat.OnStreamFunc,
	opts ...option.RequestOption,
) (*chat.Result, error) {
	ensureChatCompletionStreamIncludesUsage(&params)
	stream := client.Chat.Completions.NewStreaming(ctx, params, opts...)
	return consumeChatCompletionStream(stream, onStream)
}

func ChatStreamFromResponse(resp *http.Response, onStream chat.OnStreamFunc) (*chat.Result, error) {
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("openai chat stream response is empty")
	}
	stream := ssestream.NewStream[openai.ChatCompletionChunk](ssestream.NewDecoder(resp), nil)
	return consumeChatCompletionStream(stream, onStream)
}

func consumeChatCompletionStream(stream *ssestream.Stream[openai.ChatCompletionChunk], onStream chat.OnStreamFunc) (*chat.Result, error) {
	acc := openai.ChatCompletionAccumulator{}
	toolCalls := streamToolCallAccumulator{}
	var finalUsage *chat.Usage
	var reasoningContent strings.Builder
	rawChunks := make([]openai.ChatCompletionChunk, 0)

	for stream.Next() {
		chunk := stream.Current()
		rawChunks = append(rawChunks, chunk)
		acc.AddChunk(sanitizeChatCompletionChunkForAccumulator(chunk))
		if chunk.JSON.Usage.Valid() {
			usage := ChatCompletionUsageToChatUsage(chunk.Usage)
			finalUsage = &usage
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		if content := reasoningContentFromRawJSON(chunk.Choices[0].Delta.RawJSON()); content != "" {
			reasoningContent.WriteString(content)
		}

		delta := chunk.Choices[0].Delta.Content

		if onStream != nil && delta != "" {
			if err := onStream(chat.StreamEvent{
				Delta: delta,
				Raw:   chunk,
			}); err != nil {
				stream.Close()
				return nil, err
			}
		}

		for _, tc := range chunk.Choices[0].Delta.ToolCalls {
			toolCallDelta := toolCalls.addDelta(tc)
			if onStream != nil {
				if err := onStream(chat.StreamEvent{
					ToolCallDelta: &toolCallDelta,
					Raw:           chunk,
				}); err != nil {
					stream.Close()
					return nil, err
				}
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	completion := acc.ChatCompletion
	result := accumulatedToResult(&completion)
	applyStreamToolCallsToResult(result, toolCalls.toolCalls())
	applyReasoningContentToResult(result, reasoningContent.String())
	if finalUsage != nil {
		result.Usage = *finalUsage
	}
	result.Raw = rawChunks

	if onStream != nil {
		if err := onStream(chat.StreamEvent{
			Done:  true,
			Usage: &result.Usage,
			Raw:   rawChunks,
		}); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func sanitizeChatCompletionChunkForAccumulator(chunk openai.ChatCompletionChunk) openai.ChatCompletionChunk {
	for i := range chunk.Choices {
		if chunk.Choices[i].Index < 0 {
			chunk.Choices[i].Index = 0
		}
		for j := range chunk.Choices[i].Delta.ToolCalls {
			if chunk.Choices[i].Delta.ToolCalls[j].Index < 0 {
				chunk.Choices[i].Delta.ToolCalls[j].Index = 0
			}
		}
	}
	return chunk
}

func accumulatedToResult(resp *openai.ChatCompletion) *chat.Result {
	return ChatCompletionToResult(resp)
}

func ensureChatCompletionStreamIncludesUsage(params *openai.ChatCompletionNewParams) {
	if params == nil {
		return
	}
	params.StreamOptions.IncludeUsage = openai.Bool(true)
}

type streamToolCallAccumulator struct {
	calls []streamToolCallState
}

type streamToolCallState struct {
	ID        string
	Type      string
	Name      string
	Arguments string
}

func (acc *streamToolCallAccumulator) addDelta(delta openai.ChatCompletionChunkChoiceDeltaToolCall) chat.ToolCallDelta {
	index := int(delta.Index)
	if index < 0 {
		index = 0
	}
	acc.calls = expandStreamToolCallStates(acc.calls, index)
	call := &acc.calls[index]
	if delta.ID != "" {
		call.ID = delta.ID
	}
	if delta.Type != "" {
		call.Type = string(delta.Type)
	}
	nameDelta := call.addNameDelta(delta.Function.Name)
	call.Arguments += delta.Function.Arguments
	return chat.ToolCallDelta{
		Index:     index,
		ID:        delta.ID,
		Name:      nameDelta,
		ArgsChunk: delta.Function.Arguments,
	}
}

func (call *streamToolCallState) addNameDelta(delta string) string {
	if delta == "" {
		return ""
	}
	if call.Name == "" {
		call.Name = delta
		return delta
	}
	if delta == call.Name {
		return ""
	}
	if strings.HasPrefix(delta, call.Name) {
		nameDelta := strings.TrimPrefix(delta, call.Name)
		call.Name = delta
		return nameDelta
	}
	call.Name += delta
	return delta
}

func (acc *streamToolCallAccumulator) toolCalls() []chat.ToolCall {
	out := make([]chat.ToolCall, 0, len(acc.calls))
	for _, call := range acc.calls {
		if call.ID == "" && call.Type == "" && call.Name == "" && call.Arguments == "" {
			continue
		}
		callType := call.Type
		if callType == "" {
			callType = "function"
		}
		out = append(out, chat.ToolCall{
			ID:   call.ID,
			Type: callType,
			Function: chat.ToolCallFunction{
				Name:      call.Name,
				Arguments: call.Arguments,
			},
		})
	}
	return out
}

func applyStreamToolCallsToResult(result *chat.Result, toolCalls []chat.ToolCall) {
	if result == nil || len(toolCalls) == 0 {
		return
	}
	if len(result.ToolCalls) == 0 {
		result.ToolCalls = append([]chat.ToolCall{}, toolCalls...)
	} else {
		patchStreamToolCalls(result.ToolCalls, toolCalls)
	}
	patchedMessage := false
	for i := range result.Messages {
		if len(result.Messages[i].ToolCalls) == 0 {
			continue
		}
		patchStreamToolCalls(result.Messages[i].ToolCalls, toolCalls)
		patchedMessage = true
	}
	if !patchedMessage && len(result.ToolCalls) > 0 {
		result.Messages = append(result.Messages, chat.Message{
			Role:      chat.RoleAssistant,
			Content:   result.Text,
			ToolCalls: append([]chat.ToolCall{}, result.ToolCalls...),
		})
	}
}

func patchStreamToolCalls(dst []chat.ToolCall, src []chat.ToolCall) {
	for i := range dst {
		if i >= len(src) {
			return
		}
		if src[i].ID != "" {
			dst[i].ID = src[i].ID
		}
		if src[i].Type != "" {
			dst[i].Type = src[i].Type
		}
		if src[i].Function.Name != "" {
			dst[i].Function.Name = src[i].Function.Name
		}
		dst[i].Function.Arguments = src[i].Function.Arguments
	}
}

func expandStreamToolCallStates(calls []streamToolCallState, index int) []streamToolCallState {
	if index < len(calls) {
		return calls
	}
	next := make([]streamToolCallState, index+1)
	copy(next, calls)
	return next
}
