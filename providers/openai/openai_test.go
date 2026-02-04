package openai

import (
	"testing"

	openai "github.com/openai/openai-go/v3"
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

	params, err := buildParams(req, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(params.Model) != "gpt-4.1-mini" {
		t.Fatalf("model mismatch")
	}
	if !params.Temperature.Valid() || params.Temperature.Value != temp {
		t.Fatalf("temperature mismatch")
	}
	if !params.TopP.Valid() || params.TopP.Value != topP {
		t.Fatalf("temperature/top_p mismatch")
	}
	if !params.MaxCompletionTokens.Valid() || params.MaxCompletionTokens.Value != int64(maxTokens) {
		t.Fatalf("max completion tokens mismatch")
	}
	if len(params.Stop.OfStringArray) != 1 || params.Stop.OfStringArray[0] != "END" {
		t.Fatalf("stop mismatch")
	}
	if !params.PresencePenalty.Valid() || params.PresencePenalty.Value != presence {
		t.Fatalf("presence penalty mismatch")
	}
	if !params.FrequencyPenalty.Valid() || params.FrequencyPenalty.Value != frequency {
		t.Fatalf("penalty mismatch")
	}
	if !params.User.Valid() || params.User.Value != user {
		t.Fatalf("user mismatch")
	}
	if len(params.Tools) != 1 {
		t.Fatalf("tools not mapped")
	}
	if params.ToolChoice == (openai.ChatCompletionToolChoiceOptionUnionParam{}) {
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
	params, err := buildParams(req, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !params.MaxCompletionTokens.Valid() || params.MaxCompletionTokens.Value != int64(maxTokens) {
		t.Fatalf("expected max_completion_tokens for o1 models")
	}
}

func TestToolSchemaAddsArrayItems(t *testing.T) {
	req := &chat.Request{
		Model: "gpt-4.1-mini",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Tools: []chat.Tool{
			chat.FunctionTool("url_fetch", "desc", []byte(`{"type":"object","properties":{"body":{"type":["string","object","array","number","boolean","null"]}}}`)),
		},
	}

	params, err := buildParams(req, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(params.Tools) != 1 || params.Tools[0].OfFunction == nil {
		t.Fatalf("expected function tool parameters")
	}
	schema := params.Tools[0].OfFunction.Function.Parameters
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties map in schema")
	}
	body, ok := props["body"].(map[string]any)
	if !ok {
		t.Fatalf("expected body schema map")
	}
	if _, ok := body["items"]; !ok {
		t.Fatalf("expected items to be added for array type")
	}
}
