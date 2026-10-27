package openai

import (
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
)

func TestKimi31Parameters(t *testing.T) {
	for _, model := range []string{"kimi-k3", "kimi-k3.1-pro", "moonshotai/kimi-k3.1-pro-highspeed"} {
		for _, effort := range []chat.ReasoningEffort{"", "low", "high", "max", "xhigh", "none", "medium"} {
			for _, ttl := range []string{"5m", "1h", "30m"} {
				temperature, topP, maxTokens := 0.5, 0.8, 4096
				req := &chat.Request{Model: model, Messages: []chat.Message{chat.User("hi")},
					Options: chat.Options{Temperature: &temperature, TopP: &topP, MaxTokens: &maxTokens, ReasoningEffort: &effort,
						OpenAI: structs.JSONMap{"n": 2, "presence_penalty": 1, "frequency_penalty": 1,
							"prompt_cache_options": map[string]any{"mode": "implicit", "ttl": ttl}},
					},
				}
				params, err := buildParams(req, "")
				if effort == "none" || effort == "medium" || ttl == "30m" {
					if err == nil {
						t.Errorf("%s/%s/%s: expected error", model, effort, ttl)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if params.Temperature.Valid() || params.TopP.Valid() || params.N.Valid() || params.PresencePenalty.Valid() || params.FrequencyPenalty.Valid() || params.MaxTokens.Valid() {
					t.Fatal("unsupported Kimi parameters retained")
				}
				wantEffort := string(effort)
				if effort == "xhigh" {
					wantEffort = "max"
				}
				if string(params.ReasoningEffort) != wantEffort || params.MaxCompletionTokens.Value != int64(maxTokens) || params.PromptCacheOptions.Ttl != ttl {
					t.Fatalf("wrong Kimi parameter mapping: %#v", params)
				}
				req.Options.OpenAI["prompt_cache_options"] = map[string]any{"mode": "explicit", "ttl": ttl}
				if _, err := buildParams(req, ""); err == nil {
					t.Fatal("expected explicit Kimi cache mode to be rejected")
				}
			}
		}
	}
}
