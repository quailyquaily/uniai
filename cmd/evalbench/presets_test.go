package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/quailyquaily/uniai"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/evaluate"
)

func TestComparisonPresets(t *testing.T) {
	// Old single-model environment values must not override a selected preset.
	getenv := env(map[string]string{"EVALUATE_PROVIDER": "openai", "EVALUATE_MODEL": "old-model", "EVALUATE_EMULATION_MODE": "force", "EVALUATE_REASONING_EFFORT": "high", "EVALUATE_MAX_TOKENS": "999", "EVALUATE_INFERENCE_PROVIDER": "old-provider"})
	for _, tc := range []struct {
		name, provider, model, effort string
		tokens                        int
	}{
		{"gpt-4o-mini", "openai", "gpt-4o-mini", "", 256},
		{"jev", "typesafe", "jev-1.13.0", "", 0},
		{"gpt-5.6-luna", "openai", "gpt-5.6-luna", "none", 256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, err := parseOptions([]string{"--preset", tc.name}, getenv, &bytes.Buffer{})
			if err != nil {
				t.Fatal(err)
			}
			if o.request.Provider != tc.provider || o.request.Model != tc.model || o.request.InferenceProvider != "" {
				t.Fatal(o.request)
			}
			if tc.tokens == 0 {
				if o.request.EmulationMode != evaluate.EmulationOff || o.request.EmulationOptions != nil {
					t.Fatal(o.request)
				}
			} else {
				options := o.request.EmulationOptions
				if o.request.EmulationMode != evaluate.EmulationForce || options == nil || options.MaxTokens == nil || *options.MaxTokens != tc.tokens {
					t.Fatal(o.request)
				}
				if tc.effort == "" {
					if options.ReasoningEffort != nil {
						t.Fatal("GPT-4o mini must omit reasoning_effort")
					}
				} else if options.ReasoningEffort == nil || string(*options.ReasoningEffort) != tc.effort {
					t.Fatal(options)
				}
			}
		})
	}
	for _, args := range [][]string{{"--preset", "missing"}, {"--preset", "gpt-5.6-luna", "--max-tokens", "0"}} {
		if _, err := parseOptions(args, env(nil), &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestPresetEnvironmentAndFlagOverrides(t *testing.T) {
	o, err := parseOptions([]string{"--model", "custom-luna", "--max-tokens", "1024", "--reasoning-effort", "low"}, env(map[string]string{"EVALUATE_PRESET": "gpt-5.6-luna"}), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if o.request.Model != "custom-luna" || *o.request.EmulationOptions.MaxTokens != 1024 || *o.request.EmulationOptions.ReasoningEffort != chat.ReasoningEffortLow {
		t.Fatal(o.request)
	}
	o, err = parseOptions([]string{"--preset", "", "--provider", "openai", "--model", "custom"}, env(map[string]string{"EVALUATE_PRESET": "jev", "EVALUATE_MAX_TOKENS": "128"}), &bytes.Buffer{})
	if err != nil || o.request.Model != "custom" || *o.request.EmulationOptions.MaxTokens != 128 {
		t.Fatal(o.request, err)
	}
}

func TestOpenAIPresetHTTPParameters(t *testing.T) {
	for _, name := range []string{"gpt-4o-mini", "gpt-5.6-luna"} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["model"] != name {
					t.Error("wrong model")
				}
				if name == "gpt-4o-mini" {
					if _, ok := body["reasoning_effort"]; ok {
						t.Error("GPT-4o mini got reasoning_effort")
					}
					if body["max_completion_tokens"] != float64(256) {
						t.Error(body["max_completion_tokens"])
					}
				} else if body["reasoning_effort"] != "none" || body["max_completion_tokens"] != float64(256) {
					t.Error("Luna controls not mapped")
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"model": name, "choices": []any{map[string]any{"message": map[string]any{"content": `{"answer":true}`}, "finish_reason": "stop"}}})
			}))
			defer server.Close()
			getenv := env(map[string]string{"OPENAI_API_KEY": "key", "OPENAI_API_BASE": server.URL + "/v1"})
			o, err := parseOptions([]string{"--preset", name}, getenv, &bytes.Buffer{})
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := clientConfig(o, getenv)
			if err != nil {
				t.Fatal(err)
			}
			r := o.request
			r.State = "yes"
			r.Questions = map[string]evaluate.Question{"answer": {Kind: evaluate.Boolean, Instructions: "Is this yes?"}}
			if _, err := uniai.New(cfg).Evaluate(context.Background(), r); err != nil {
				t.Fatal(err)
			}
		})
	}
}
