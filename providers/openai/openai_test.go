package openai

import (
	"testing"

	"github.com/quailyquaily/uniai/chat"
)

func TestBuildRequestMapping(t *testing.T) {
	temp := 0.4
	topP := 0.8
	maxTokens := 256
	presence := 0.1
	frequency := 0.2
	user := "end-user-1"

	req := &chat.Request{
		Model: "gpt-4.1-mini",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			Temperature:      &temp,
			TopP:             &topP,
			MaxTokens:        &maxTokens,
			Stop:             []string{"END"},
			PresencePenalty:  &presence,
			FrequencyPenalty: &frequency,
			User:             &user,
		},
		Tools: []chat.Tool{
			chat.FunctionTool("get_weather", "desc", []byte(`{"type":"object"}`)),
		},
		ToolChoice: func() *chat.ToolChoice {
			c := chat.ToolChoiceFunction("get_weather")
			return &c
		}(),
	}

	payload, err := buildRequest(req, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload.Model != "gpt-4.1-mini" {
		t.Fatalf("model mismatch")
	}
	if payload.Temperature != float32(temp) || payload.TopP != float32(topP) {
		t.Fatalf("temperature/top_p mismatch")
	}
	if payload.MaxCompletionTokens != maxTokens {
		t.Fatalf("max completion tokens mismatch")
	}
	if len(payload.Stop) != 1 || payload.Stop[0] != "END" {
		t.Fatalf("stop mismatch")
	}
	if payload.PresencePenalty != float32(presence) || payload.FrequencyPenalty != float32(frequency) {
		t.Fatalf("penalty mismatch")
	}
	if payload.User != user {
		t.Fatalf("user mismatch")
	}
	if len(payload.Tools) != 1 {
		t.Fatalf("tools not mapped")
	}
	if payload.ToolChoice == nil {
		t.Fatalf("tool choice not mapped")
	}
}

func TestMaxCompletionTokensHeuristic(t *testing.T) {
	req := &chat.Request{
		Model: "o1-mini",
		Messages: []chat.Message{
			chat.User("hello"),
		},
	}
	maxTokens := 128
	req.Options.MaxTokens = &maxTokens
	payload, err := buildRequest(req, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload.MaxCompletionTokens != maxTokens {
		t.Fatalf("expected max_completion_tokens for o1 models")
	}
}
