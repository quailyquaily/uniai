package oaicompat

import (
	"bytes"
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
	reasoningDetails bool,
	onStream chat.OnStreamFunc,
	opts ...option.RequestOption,
) (*chat.Result, error) {
	ensureChatCompletionStreamIncludesUsage(&params)
	var resp *http.Response
	opts = append(opts, option.WithJSONSet("stream", true))
	if err := client.Execute(ctx, http.MethodPost, "chat/completions", params, &resp, opts...); err != nil {
		return nil, err
	}
	return ChatStreamFromResponse(resp, reasoningDetails, onStream)
}

// The SDK consumes past [DONE] until EOF. Stop at the protocol boundary and
// retain it so endpoints that omit finish_reason can still signal completion.
type chatCompletionDecoder struct {
	ssestream.Decoder
	done bool
}

func (d *chatCompletionDecoder) Next() bool {
	if d.done {
		return false
	}
	for d.Decoder.Next() {
		data := bytes.TrimSpace(d.Event().Data)
		if len(data) == 0 {
			continue
		}
		if bytes.Equal(data, []byte("[DONE]")) {
			d.done = true
			return false
		}
		return true
	}
	return false
}

func ChatStreamFromResponse(resp *http.Response, reasoningDetails bool, onStream chat.OnStreamFunc) (*chat.Result, error) {
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("openai chat stream response is empty")
	}
	decoder := &chatCompletionDecoder{Decoder: ssestream.NewDecoder(resp)}
	stream := ssestream.NewStream[openai.ChatCompletionChunk](decoder, nil)
	defer stream.Close()
	acc := openai.ChatCompletionAccumulator{}
	toolCalls := streamToolCallAccumulator{}
	var finalUsage *chat.Usage
	var reasoningContent strings.Builder
	rawChunks := make([]openai.ChatCompletionChunk, 0)
	finishedChoices := map[int64]bool{}

	for stream.Next() {
		chunk := stream.Current()
		rawChunks = append(rawChunks, chunk)
		acc.AddChunk(sanitizeChatCompletionChunkForAccumulator(chunk))
		for _, choice := range chunk.Choices {
			finishedChoices[choice.Index] = finishedChoices[choice.Index] || choice.FinishReason != ""
		}
		if chunk.JSON.Usage.Valid() {
			usage := ChatCompletionUsageToChatUsage(chunk.Usage)
			finalUsage = &usage
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		if content := reasoningContentFromRawJSON(chunk.Choices[0].Delta.RawJSON()); content != "" {
			reasoningContent.WriteString(content)
			if reasoningDetails && onStream != nil {
				if err := onStream(chat.StreamEvent{
					ReasoningDelta: &chat.ReasoningDelta{
						Index: 0,
						Type:  chat.ReasoningDeltaThinking,
						Delta: content,
					},
					Raw: chunk,
				}); err != nil {
					return nil, err
				}
			}
		}

		delta := chunk.Choices[0].Delta.Content

		if onStream != nil && delta != "" {
			if err := onStream(chat.StreamEvent{
				Delta: delta,
				Raw:   chunk,
			}); err != nil {
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
					return nil, err
				}
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}
	if !decoder.done {
		finished := len(finishedChoices) > 0
		for _, complete := range finishedChoices {
			finished = finished && complete
		}
		if !finished {
			return nil, fmt.Errorf("openai chat stream ended without finish_reason or [DONE]")
		}
	}

	result := ChatCompletionToResult(&acc.ChatCompletion)
	applyStreamToolCallsToResult(result, toolCalls.toolCalls())
	applyReasoningContentToResult(result, reasoningContent.String())
	if reasoningDetails {
		ApplyReasoningDetails(result)
	}
	if finalUsage != nil {
		result.Usage = *finalUsage
	}
	result.Raw = rawChunks

	if onStream != nil {
		if err := onStream(chat.StreamEvent{
			FinishReason: result.FinishReason,
			Done:         true,
			Usage:        &result.Usage,
			Raw:          rawChunks,
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
