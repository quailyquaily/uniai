package openai

import (
	"testing"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"
	"github.com/quailyquaily/uniai/chat"
)

func TestToChatOptions(t *testing.T) {
	req := openai.ChatCompletionNewParams{
		Model: openai.ChatModel("gpt-4.1-mini"),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("hello"),
		},
		Temperature:      openai.Float(0.7),
		TopP:             openai.Float(0.9),
		MaxTokens:        openai.Int(123),
		Stop:             openai.ChatCompletionNewParamsStopUnion{OfStringArray: []string{"END"}},
		PresencePenalty:  openai.Float(0.1),
		FrequencyPenalty: openai.Float(0.2),
		User:             openai.String("u1"),
		Tools: []openai.ChatCompletionToolUnionParam{
			openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
				Name:        "get_weather",
				Description: openai.String("desc"),
				Parameters:  shared.FunctionParameters(map[string]any{"type": "object"}),
			}),
		},
		ToolChoice: openai.ToolChoiceOptionFunctionToolChoice(openai.ChatCompletionNamedToolChoiceFunctionParam{
			Name: "get_weather",
		}),
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

func TestToChatOptionsWithUserImageParts(t *testing.T) {
	req := openai.ChatCompletionNewParams{
		Model: openai.ChatModel("gpt-5.2"),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage([]openai.ChatCompletionContentPartUnionParam{
				openai.TextContentPart("describe"),
				openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
					URL: "https://example.com/a.png",
				}),
			}),
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
	if len(chatReq.Messages) != 1 {
		t.Fatalf("expected one message, got %d", len(chatReq.Messages))
	}
	msg := chatReq.Messages[0]
	if len(msg.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(msg.Parts))
	}
	if msg.Parts[0].Type != chat.PartTypeText || msg.Parts[0].Text != "describe" {
		t.Fatalf("unexpected first part: %#v", msg.Parts[0])
	}
	if msg.Parts[1].Type != chat.PartTypeImageURL || msg.Parts[1].URL != "https://example.com/a.png" {
		t.Fatalf("unexpected second part: %#v", msg.Parts[1])
	}
}
