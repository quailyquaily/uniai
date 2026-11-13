package gemini

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
)

func TestGemini4ReasoningEffort(t *testing.T) {
	allEfforts := []chat.ReasoningEffort{"none", "minimal", "low", "medium", "high", "xhigh", "max"}
	for _, tt := range []struct {
		model   string
		allowed []chat.ReasoningEffort
	}{
		{"gemini-4-carbon", []chat.ReasoningEffort{"low", "medium", "high"}},
		{"models/gemini-4-argon", []chat.ReasoningEffort{"low", "medium", "high"}},
		{"gemini-4-hydrogen", []chat.ReasoningEffort{"low", "medium", "high", "xhigh"}},
	} {
		for _, effort := range allEfforts {
			effort := effort
			req := &chat.Request{Messages: []chat.Message{chat.User("hello")}, Options: chat.Options{ReasoningEffort: &effort, ReasoningDetails: true}}
			body, err := buildRequest(req, tt.model)
			if !containsEffort(tt.allowed, effort) {
				if err == nil || !strings.Contains(err.Error(), string(effort)) {
					t.Errorf("%s/%s: expected local error, got %v", tt.model, effort, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("%s/%s: %v", tt.model, effort, err)
			}
			thinking := body.GenerationConfig.ThinkingConfig
			if thinking == nil || thinking.ThinkingLevel != string(effort) || thinking.ThinkingBudget != nil ||
				thinking.IncludeThoughts == nil || !*thinking.IncludeThoughts {
				t.Errorf("%s/%s: wrong thinking config %#v", tt.model, effort, thinking)
			}
		}

		budget := 4096
		req := &chat.Request{Messages: []chat.Message{chat.User("hello")}, Options: chat.Options{ReasoningBudget: &budget}}
		if _, err := buildRequest(req, tt.model); err == nil || !strings.Contains(err.Error(), "budget") {
			t.Errorf("%s: expected reasoning budget error, got %v", tt.model, err)
		}
	}
}

func TestGemini4OmitsUnsetThinkingLevel(t *testing.T) {
	req := &chat.Request{Messages: []chat.Message{chat.User("hello")}, Options: chat.Options{ReasoningDetails: true}}
	body, err := buildRequest(req, "gemini-4-hydrogen")
	if err != nil {
		t.Fatal(err)
	}
	thinking := body.GenerationConfig.ThinkingConfig
	if thinking == nil || thinking.ThinkingLevel != "" || thinking.IncludeThoughts == nil || !*thinking.IncludeThoughts {
		t.Fatalf("expected includeThoughts with the model's default level, got %#v", thinking)
	}
}

func TestGemini4DropsSampling(t *testing.T) {
	temperature, topP, maxTokens := 0.2, 0.9, 32768
	raw := structs.JSONMap{"top_k": 40}
	opts := chat.Options{Temperature: &temperature, TopP: &topP, MaxTokens: &maxTokens, Stop: []string{"END"}, OpenAI: raw}
	for _, model := range []string{"gemini-4-carbon", "gemini-4-argon", "gemini-4-hydrogen"} {
		body, err := buildRequest(&chat.Request{Messages: []chat.Message{chat.User("hi")}, Options: opts}, model)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(body.GenerationConfig)
		got := string(data)
		for _, field := range []string{"temperature", "topP", "topK"} {
			if strings.Contains(got, field) {
				t.Errorf("%s: %s should be omitted: %s", model, field, got)
			}
		}
		if !strings.Contains(got, `"maxOutputTokens":32768`) || !strings.Contains(got, `"stopSequences":["END"]`) {
			t.Errorf("%s: expected output limit and stops to remain: %s", model, got)
		}
	}
	if *opts.Temperature != 0.2 || *opts.TopP != 0.9 || raw["top_k"] != 40 {
		t.Fatal("caller options were mutated")
	}

	// Sampling-only requests should not send an empty generationConfig.
	body, err := buildRequest(&chat.Request{Messages: []chat.Message{chat.User("hi")}, Options: chat.Options{Temperature: &temperature}}, "gemini-4-argon")
	if err != nil {
		t.Fatal(err)
	}
	if body.GenerationConfig != nil {
		t.Fatalf("expected no generationConfig, got %#v", body.GenerationConfig)
	}

	// Earlier models keep sampling fields.
	body, err = buildRequest(&chat.Request{Messages: []chat.Message{chat.User("hi")}, Options: opts}, "gemini-3.8-flash")
	if err != nil {
		t.Fatal(err)
	}
	if body.GenerationConfig.Temperature == nil || body.GenerationConfig.TopP == nil || body.GenerationConfig.TopK == nil {
		t.Fatalf("gemini-3.8-flash should keep sampling: %#v", body.GenerationConfig)
	}
}

func containsEffort(list []chat.ReasoningEffort, effort chat.ReasoningEffort) bool {
	for _, v := range list {
		if v == effort {
			return true
		}
	}
	return false
}
