package oaicompat

import (
	"encoding/json"

	openai "github.com/openai/openai-go/v3"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/jsonoutput"
)

// ChatCompletionToResult converts an OpenAI-compatible Chat Completions response
// into the unified chat result while preserving assistant message replay fields.
func ChatCompletionToResult(resp *openai.ChatCompletion) *chat.Result {
	if resp == nil {
		return &chat.Result{Warnings: []string{"response is nil"}}
	}
	text := ""
	parts := make([]chat.Part, 0, 1)
	messages := make([]chat.Message, 0, len(resp.Choices))
	var toolCalls []chat.ToolCall
	for _, choice := range resp.Choices {
		messageToolCalls := ToToolCalls(choice.Message.ToolCalls)
		content := choice.Message.Content
		if normalized, ok := jsonoutput.NormalizeSingleJSONContent(content); ok {
			content = normalized
		}
		text += content
		if len(messageToolCalls) > 0 && len(toolCalls) == 0 {
			toolCalls = messageToolCalls
		}
		message := chat.Message{
			Role:             chat.RoleAssistant,
			Content:          content,
			ToolCalls:        messageToolCalls,
			ReasoningContent: reasoningContentFromRawJSON(choice.Message.RawJSON()),
		}
		if message.Content != "" || len(message.ToolCalls) > 0 || message.ReasoningContent != "" {
			messages = append(messages, message)
		}
	}
	if text != "" {
		parts = append(parts, chat.TextPart(text))
	}

	return &chat.Result{
		Text:      text,
		Parts:     parts,
		Model:     resp.Model,
		Messages:  messages,
		ToolCalls: toolCalls,
		Usage:     ChatCompletionUsageToChatUsage(resp.Usage),
		Raw:       resp,
	}
}

func reasoningContentFromRawJSON(raw string) string {
	if raw == "" {
		return ""
	}
	var payload struct {
		ReasoningContent string `json:"reasoning_content"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}
	return payload.ReasoningContent
}

func applyReasoningContentToResult(result *chat.Result, reasoningContent string) {
	if result == nil || reasoningContent == "" {
		return
	}
	for i := range result.Messages {
		if result.Messages[i].Role == chat.RoleAssistant {
			result.Messages[i].ReasoningContent = reasoningContent
			return
		}
	}
	result.Messages = append(result.Messages, chat.Message{
		Role:             chat.RoleAssistant,
		Content:          result.Text,
		ToolCalls:        chat.CloneToolCalls(result.ToolCalls),
		ReasoningContent: reasoningContent,
	})
}
