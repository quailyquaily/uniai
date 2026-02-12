package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/quailyquaily/uniai"
	"gopkg.in/yaml.v3"
)

func loadConfig(path string) (*fileConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg fileConfig
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	if len(cfg.Tests) == 0 {
		return nil, fmt.Errorf("config has no tests")
	}

	seen := map[string]struct{}{}
	for i, t := range cfg.Tests {
		if strings.TrimSpace(t.Name) == "" {
			return nil, fmt.Errorf("tests[%d].name is required", i)
		}
		if _, ok := seen[t.Name]; ok {
			return nil, fmt.Errorf("duplicate test name %q", t.Name)
		}
		seen[t.Name] = struct{}{}
	}

	return &cfg, nil
}

func selectTests(all []testConfig, selectedName string) ([]testConfig, error) {
	if selectedName == "" {
		return all, nil
	}

	for _, t := range all {
		if t.Name == selectedName {
			return []testConfig{t}, nil
		}
	}
	return nil, fmt.Errorf("test %q not found in config", selectedName)
}

func buildClientConfig(
	provider string,
	model string,
	t testConfig,
) (cfg uniai.Config, keyRefUsed, accountIDRefUsed, tokenRefUsed, setupErr string) {
	switch provider {
	case "cloudflare":
		accountIDRef := strings.TrimSpace(t.CloudflareAccountIDRef)
		if accountIDRef == "" {
			return uniai.Config{}, "", "", "", "cloudflare_account_id_ref is required for cloudflare provider"
		}
		accountID := strings.TrimSpace(os.Getenv(accountIDRef))
		if accountID == "" {
			return uniai.Config{}, "", "", "", fmt.Sprintf("env %s is empty", accountIDRef)
		}

		tokenRef := strings.TrimSpace(t.CloudflareAPITokenRef)
		if tokenRef == "" {
			tokenRef = strings.TrimSpace(t.APIKeyRef)
		}
		if tokenRef == "" {
			return uniai.Config{}, "", "", "", "cloudflare_api_token_ref or api_key_ref is required for cloudflare provider"
		}
		token := strings.TrimSpace(os.Getenv(tokenRef))
		if token == "" {
			return uniai.Config{}, "", "", "", fmt.Sprintf("env %s is empty", tokenRef)
		}

		return uniai.Config{
			Provider:            provider,
			CloudflareAccountID: accountID,
			CloudflareAPIToken:  token,
			CloudflareAPIBase:   t.APIBase,
			OpenAIModel:         model,
		}, "", accountIDRef, tokenRef, ""

	default:
		keyRef := strings.TrimSpace(t.APIKeyRef)
		if keyRef == "" {
			return uniai.Config{}, "", "", "", "api_key_ref is required"
		}
		apiKey := strings.TrimSpace(os.Getenv(keyRef))
		if apiKey == "" {
			return uniai.Config{}, "", "", "", fmt.Sprintf("env %s is empty", keyRef)
		}

		return uniai.Config{
			Provider:      provider,
			OpenAIAPIKey:  apiKey,
			OpenAIAPIBase: t.APIBase,
			OpenAIModel:   model,
		}, keyRef, "", "", ""
	}
}
