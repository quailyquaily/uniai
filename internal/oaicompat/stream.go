package oaicompat

import (
	"context"
	"strings"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
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
	acc := openai.ChatCompletionAccumulator{}
	var finalUsage *chat.Usage
	var reasoningContent strings.Builder
	rawChunks := make([]openai.ChatCompletionChunk, 0)

	for stream.Next() {
		chunk := stream.Current()
		rawChunks = append(rawChunks, chunk)
		acc.AddChunk(chunk)
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

		if delta != "" {
			if err := onStream(chat.StreamEvent{
				Delta: delta,
				Raw:   chunk,
			}); err != nil {
				stream.Close()
				return nil, err
			}
		}

		for _, tc := range chunk.Choices[0].Delta.ToolCalls {
			if err := onStream(chat.StreamEvent{
				ToolCallDelta: &chat.ToolCallDelta{
					Index:     int(tc.Index),
					ID:        tc.ID,
					Name:      tc.Function.Name,
					ArgsChunk: tc.Function.Arguments,
				},
				Raw: chunk,
			}); err != nil {
				stream.Close()
				return nil, err
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	completion := acc.ChatCompletion
	result := accumulatedToResult(&completion)
	applyReasoningContentToResult(result, reasoningContent.String())
	if finalUsage != nil {
		result.Usage = *finalUsage
	}
	result.Raw = rawChunks

	if err := onStream(chat.StreamEvent{
		Done:  true,
		Usage: &result.Usage,
		Raw:   rawChunks,
	}); err != nil {
		return nil, err
	}

	return result, nil
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
