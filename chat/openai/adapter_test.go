package openai

import (
	"testing"

	"github.com/quailyquaily/uniai/chat"
	openaiapi "github.com/sashabaranov/go-openai"
)

func TestToChatOptions(t *testing.T) {
	req := openaiapi.ChatCompletionRequest{
		Model: "gpt-4.1-mini",
		Messages: []openaiapi.ChatCompletionMessage{
			{Role: openaiapi.ChatMessageRoleUser, Content: "hello"},
		},
		Temperature:      0.7,
		TopP:             0.9,
		MaxTokens:        123,
		Stop:             []string{"END"},
		PresencePenalty:  0.1,
		FrequencyPenalty: 0.2,
		User:             "u1",
		Tools: []openaiapi.Tool{{
			Type: openaiapi.ToolTypeFunction,
			Function: &openaiapi.FunctionDefinition{
				Name:        "get_weather",
				Description: "desc",
				Parameters:  map[string]any{"type": "object"},
			},
		}},
		ToolChoice: openaiapi.ToolChoice{
			Type: openaiapi.ToolTypeFunction,
			Function: openaiapi.ToolFunction{
				Name: "get_weather",
			},
		},
	}

	opts, err := toChatOptions(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	chatReq, err := chat.BuildRequest(opts...)
	if err != nil {
		t.Fatalf("unexpected build error: %v", err)
	}
	if chatReq.Model != "gpt-4.1-mini" {
		t.Fatalf("model mismatch")
	}
	if len(chatReq.Messages) != 1 || chatReq.Messages[0].Content != "hello" {
		t.Fatalf("messages mismatch")
	}
	if chatReq.Options.MaxTokens == nil || *chatReq.Options.MaxTokens != 123 {
		t.Fatalf("max tokens mismatch")
	}
	if chatReq.ToolChoice == nil || chatReq.ToolChoice.FunctionName != "get_weather" {
		t.Fatalf("tool choice mismatch")
	}
	if len(chatReq.Tools) != 1 {
		t.Fatalf("tools mismatch")
	}
}
