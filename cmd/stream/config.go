package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/quailyquaily/uniai"
	"gopkg.in/yaml.v3"
)

type fileConfig struct {
	Prompt         string       `yaml:"prompt"`
	MaxTokens      int          `yaml:"max_tokens"`
	TimeoutSeconds int          `yaml:"timeout_seconds"`
	Tests          []testConfig `yaml:"tests"`
}

type testConfig struct {
	Name                  string `yaml:"name"`
	Provider              string `yaml:"provider"`
	APIBase               string `yaml:"api_base"`
	APIKeyRef             string `yaml:"api_key_ref"`
	Model                 string `yaml:"model"`
	Prompt                string `yaml:"prompt"`
	MaxTokens             int    `yaml:"max_tokens"`
	TimeoutSeconds        int    `yaml:"timeout_seconds"`
	ReasoningEffort       string `yaml:"reasoning_effort"`
	ReasoningBudgetTokens *int   `yaml:"reasoning_budget_tokens"`
}

func loadConfig(path string) (*fileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg fileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	if len(cfg.Tests) == 0 {
		return nil, fmt.Errorf("config has no tests")
	}
	if cfg.MaxTokens < 0 {
		return nil, fmt.Errorf("max_tokens must be >= 0")
	}
	if cfg.TimeoutSeconds < 0 {
		return nil, fmt.Errorf("timeout_seconds must be >= 0")
	}

	seen := make(map[string]struct{}, len(cfg.Tests))
	for i := range cfg.Tests {
		test := &cfg.Tests[i]
		test.Name = strings.TrimSpace(test.Name)
		test.Provider = strings.ToLower(strings.TrimSpace(test.Provider))
		test.APIBase = strings.TrimSpace(test.APIBase)
		test.APIKeyRef = strings.TrimSpace(test.APIKeyRef)
		test.Model = strings.TrimSpace(test.Model)
		test.ReasoningEffort = strings.ToLower(strings.TrimSpace(test.ReasoningEffort))

		if test.Name == "" {
			return nil, fmt.Errorf("tests[%d].name is required", i)
		}
		if _, ok := seen[test.Name]; ok {
			return nil, fmt.Errorf("duplicate test name %q", test.Name)
		}
		seen[test.Name] = struct{}{}
		if test.Provider == "" {
			test.Provider = defaultProviderName
		}
		if test.APIKeyRef == "" {
			return nil, fmt.Errorf("tests[%d].api_key_ref is required", i)
		}
		if test.Model == "" {
			return nil, fmt.Errorf("tests[%d].model is required", i)
		}
		if test.MaxTokens < 0 {
			return nil, fmt.Errorf("tests[%d].max_tokens must be >= 0", i)
		}
		if test.TimeoutSeconds < 0 {
			return nil, fmt.Errorf("tests[%d].timeout_seconds must be >= 0", i)
		}
		if test.ReasoningEffort != "" && test.ReasoningBudgetTokens != nil {
			return nil, fmt.Errorf("tests[%d].reasoning_effort and reasoning_budget_tokens cannot be used together", i)
		}
		if !validReasoningEffort(test.ReasoningEffort) {
			return nil, fmt.Errorf("tests[%d].reasoning_effort %q is invalid", i, test.ReasoningEffort)
		}
	}

	return &cfg, nil
}

func selectTests(all []testConfig, selectedName string) ([]testConfig, error) {
	selectedName = strings.TrimSpace(selectedName)
	if selectedName == "" {
		return all, nil
	}
	for _, test := range all {
		if test.Name == selectedName {
			return []testConfig{test}, nil
		}
	}
	return nil, fmt.Errorf("test %q not found in config", selectedName)
}

func buildClientConfig(test testConfig) (uniai.Config, error) {
	provider := strings.ToLower(strings.TrimSpace(test.Provider))
	if provider == "" {
		provider = defaultProviderName
	}
	apiKeyRef := strings.TrimSpace(test.APIKeyRef)
	if apiKeyRef == "" {
		return uniai.Config{}, fmt.Errorf("api_key_ref is required")
	}
	apiKey := strings.TrimSpace(os.Getenv(apiKeyRef))
	if apiKey == "" {
		return uniai.Config{}, fmt.Errorf("env %s is empty", apiKeyRef)
	}
	model := strings.TrimSpace(test.Model)
	if model == "" {
		return uniai.Config{}, fmt.Errorf("model is required")
	}
	apiBase := strings.TrimSpace(test.APIBase)

	switch provider {
	case "gemini":
		return uniai.Config{
			Provider:      provider,
			GeminiAPIKey:  apiKey,
			GeminiAPIBase: apiBase,
			GeminiModel:   model,
		}, nil
	case "anthropic":
		return uniai.Config{
			Provider:         provider,
			AnthropicAPIKey:  apiKey,
			AnthropicAPIBase: apiBase,
			AnthropicModel:   model,
		}, nil
	case "openai", "openai_resp", "deepseek", "sakana", "xai", "groq", "meta":
		return uniai.Config{
			Provider:      provider,
			OpenAIAPIKey:  apiKey,
			OpenAIAPIBase: apiBase,
			OpenAIModel:   model,
		}, nil
	default:
		return uniai.Config{}, fmt.Errorf("provider %q is not supported by cmd/stream", provider)
	}
}

func validReasoningEffort(effort string) bool {
	switch effort {
	case "", "none", "minimal", "low", "medium", "high", "max", "xhigh":
		return true
	default:
		return false
	}
}
