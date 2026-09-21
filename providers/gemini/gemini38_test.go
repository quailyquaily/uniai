package gemini

import (
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/chat"
)

func TestGemini38ReasoningEffort(t *testing.T) {
	for _, model := range []string{"gemini-3.8-flash", "models/gemini-3.8-flash", "gemini-3-flash-preview"} {
		for _, effort := range []chat.ReasoningEffort{"low", "medium", "high", "minimal"} {
			req := &chat.Request{Messages: []chat.Message{chat.User("hello")}, Options: chat.Options{ReasoningEffort: &effort, ReasoningDetails: true}}
			body, err := buildRequest(req, model)
			if strings.Contains(model, "3.8") && effort == "minimal" {
				if err == nil || !strings.Contains(err.Error(), "minimal") {
					t.Errorf("%s: expected minimal effort error, got %v", model, err)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if body.GenerationConfig == nil || body.GenerationConfig.ThinkingConfig == nil {
				t.Fatal("missing thinking config")
			}
			thinking := body.GenerationConfig.ThinkingConfig
			if thinking.ThinkingLevel != string(effort) || thinking.IncludeThoughts == nil || !*thinking.IncludeThoughts {
				t.Errorf("wrong thinking config: %#v", thinking)
			}
		}
	}
}
