package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
)

func TestSonnet55BetweenTools(t *testing.T) {
	for _, model := range []string{"claude-sonnet-5-5", "models/claude-sonnet-5-5", "anthropic/claude-sonnet-5.5"} {
		for _, effort := range []chat.ReasoningEffort{"", "low", "medium", "high", "xhigh", "max"} {
			for _, details := range []bool{false, true} {
				req := &chat.Request{Messages: []chat.Message{chat.User("hello")}, Options: chat.Options{
					Anthropic: structs.JSONMap{"thinking_type": "between_tools"}, ReasoningDetails: details,
				}}
				if effort != "" {
					req.Options.ReasoningEffort = &effort
				}
				body, err := buildRequest(req, model)
				if effort == "xhigh" || effort == "max" {
					if err == nil || !strings.Contains(err.Error(), "between_tools") {
						t.Errorf("%s/%s: expected between_tools error, got %v", model, effort, err)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				data, err := json.Marshal(body.Thinking)
				if err != nil || string(data) != `{"type":"between_tools"}` {
					t.Errorf("%s/%s: unexpected thinking: %s, %v", model, effort, data, err)
				}
			}
		}
	}
}

func TestSonnet55ThinkingTypeValidation(t *testing.T) {
	for _, tt := range []struct{ model, mode string }{
		{"claude-sonnet-5-5", "disabled"},
		{"claude-sonnet-5-5", "enabled"},
		{"claude-sonnet-5-5", "invalid"},
		{"claude-opus-5-5", "between_tools"},
		{"claude-sonnet-5", "between_tools"},
	} {
		req := &chat.Request{Messages: []chat.Message{chat.User("hello")}, Options: chat.Options{
			Anthropic: structs.JSONMap{"thinking_type": tt.mode},
		}}
		if _, err := buildRequest(req, tt.model); err == nil {
			t.Errorf("%s/%s: expected thinking type error", tt.model, tt.mode)
		}
	}
}
