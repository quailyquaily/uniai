package oaicompat

import (
	"encoding/json"
	"testing"

	openai "github.com/openai/openai-go/v3"
)

func TestChatCompletionToResultPreservesRefusal(t *testing.T) {
	var response openai.ChatCompletion
	if err := json.Unmarshal([]byte(`{"choices":[{"message":{"content":"{}","refusal":"refused"},"finish_reason":"stop"}]}`), &response); err != nil {
		t.Fatal(err)
	}
	out := ChatCompletionToResult(&response)
	if out.FinishReason != "content_filter" {
		t.Fatalf("refusal lost: %+v", out)
	}
}
