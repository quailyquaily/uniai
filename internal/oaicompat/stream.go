package oaicompat

import (
	"context"

	openai "github.com/openai/openai-go/v3"
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
) (*chat.Result, error) {
	ensureChatCompletionStreamIncludesUsage(&params)
	stream := client.Chat.Completions.NewStreaming(ctx, params)
	acc := openai.ChatCompletionAccumulator{}
	var finalUsage *chat.Usage

	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)
		if chunk.JSON.Usage.Valid() {
			usage := ChatCompletionUsageToChatUsage(chunk.Usage)
			finalUsage = &usage
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		delta := chunk.Choices[0].Delta.Content

		if delta != "" {
			if err := onStream(chat.StreamEvent{Delta: delta}); err != nil {
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
	if finalUsage != nil {
		result.Usage = *finalUsage
	}

	if err := onStream(chat.StreamEvent{
		Done:  true,
		Usage: &result.Usage,
	}); err != nil {
		return nil, err
	}

	return result, nil
}

func accumulatedToResult(resp *openai.ChatCompletion) *chat.Result {
	if resp == nil {
		return &chat.Result{Warnings: []string{"response is nil"}}
	}
	text := ""
	parts := make([]chat.Part, 0, 1)
	var toolCalls []chat.ToolCall
	for _, choice := range resp.Choices {
		text += choice.Message.Content
		if len(choice.Message.ToolCalls) > 0 && len(toolCalls) == 0 {
			toolCalls = ToToolCalls(choice.Message.ToolCalls)
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
		Usage:     ChatCompletionUsageToChatUsage(resp.Usage),
		Raw:       resp,
	}
}

func ensureChatCompletionStreamIncludesUsage(params *openai.ChatCompletionNewParams) {
	if params == nil {
		return
	}
	params.StreamOptions.IncludeUsage = openai.Bool(true)
}
