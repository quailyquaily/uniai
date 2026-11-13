package openai

import (
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
)

// Gemini's OpenAI-compatible endpoint maps minimal to low for Gemini 4,
// accepts xhigh only on Hydrogen, and cannot disable reasoning.
func TestGemini4OpenAICompatParameters(t *testing.T) {
	for _, tt := range []struct {
		model   string
		allowed []string
	}{
		{"gemini-4-carbon", []string{"", "minimal", "low", "medium", "high"}},
		{"gemini-4-argon", []string{"", "minimal", "low", "medium", "high"}},
		{"google/gemini-4-hydrogen", []string{"", "minimal", "low", "medium", "high", "xhigh"}},
	} {
		for _, effort := range []chat.ReasoningEffort{"", "none", "minimal", "low", "medium", "high", "xhigh", "max"} {
			temperature, topP := 0.4, 0.7
			req := &chat.Request{Model: tt.model, Messages: []chat.Message{chat.User("hi")},
				Options: chat.Options{Temperature: &temperature, TopP: &topP}}
			if effort != "" {
				req.Options.ReasoningEffort = &effort
			}
			params, err := buildParams(req, "")
			if !containsString(tt.allowed, string(effort)) {
				if err == nil || !strings.Contains(err.Error(), "reasoning effort") {
					t.Errorf("%s/%q: expected local error, got %v", tt.model, effort, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("%s/%q: %v", tt.model, effort, err)
			}
			if string(params.ReasoningEffort) != string(effort) {
				t.Errorf("%s/%q: reasoning_effort = %q", tt.model, effort, params.ReasoningEffort)
			}
			if params.Temperature.Valid() || params.TopP.Valid() {
				t.Errorf("%s/%q: sampling should be omitted", tt.model, effort)
			}
			if *req.Options.Temperature != 0.4 || *req.Options.TopP != 0.7 {
				t.Fatal("caller options were mutated")
			}
		}
	}

	// Raw options must not bypass the effort restriction.
	req := &chat.Request{Model: "gemini-4-carbon", Messages: []chat.Message{chat.User("hi")},
		Options: chat.Options{OpenAI: structs.JSONMap{"reasoning_effort": "xhigh"}}}
	if _, err := buildParams(req, ""); err == nil {
		t.Fatal("expected raw xhigh reasoning_effort to be rejected for gemini-4-carbon")
	}
}

func containsString(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
