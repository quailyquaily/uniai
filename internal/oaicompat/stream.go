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
	stream := client.Chat.Completions.NewStreaming(ctx, params)
	acc := openai.ChatCompletionAccumulator{}

	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)

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

	if err := onStream(chat.StreamEvent{
		Done: true,
		Usage: &chat.Usage{
			InputTokens:  int(completion.Usage.PromptTokens),
			OutputTokens: int(completion.Usage.CompletionTokens),
			TotalTokens:  int(completion.Usage.TotalTokens),
		},
	}); err != nil {
		return nil, err
	}

	return accumulatedToResult(&completion), nil
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
		Usage: chat.Usage{
			InputTokens:  int(resp.Usage.PromptTokens),
			OutputTokens: int(resp.Usage.CompletionTokens),
			TotalTokens:  int(resp.Usage.TotalTokens),
		},
		Raw: resp,
	}
}
