package uniai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestChatEchoJSON(t *testing.T) {
	cfg, provider, model, ok := pickChatConfig()
	if !ok {
		t.Skip("no chat provider env configured")
	}

	client := New(cfg)
	payload := `{"ok":true,"echo":"ping"}`

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := client.Chat(ctx,
		WithProvider(provider),
		WithModel(model),
		WithMessages(
			System("Return exactly the JSON object from the user message, and nothing else."),
			User("echo json: "+payload),
		),
		WithTemperature(0),
	)
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	if resp == nil || strings.TrimSpace(resp.Text) == "" {
		t.Fatalf("empty chat response")
	}

	obj, err := parseJSONObject(resp.Text)
	if err != nil {
		t.Fatalf("expected JSON response, got: %q (err: %v)", resp.Text, err)
	}
	if okVal, ok := obj["ok"].(bool); !ok || !okVal {
		t.Fatalf("expected ok=true, got: %#v", obj["ok"])
	}
	if obj["echo"] != "ping" {
		t.Fatalf("expected echo=ping, got: %#v", obj["echo"])
	}
}

func TestOtherFeatures(t *testing.T) {
	t.Run("embedding", func(t *testing.T) {
		cfg, provider, model, ok := pickEmbeddingConfig()
		if !ok {
			t.Skip("no embedding provider env configured")
		}

		client := New(cfg)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		resp, err := client.Embedding(ctx,
			Embedding(model, "hello"),
			WithEmbeddingProvider(provider),
		)
		if err != nil {
			t.Fatalf("embedding failed: %v", err)
		}
		if resp == nil || len(resp.Data) == 0 {
			t.Fatalf("expected embedding data, got: %#v", resp)
		}
	})

	t.Run("classify", func(t *testing.T) {
		cfg, ok := pickJinaConfig()
		if !ok {
			t.Skip("no jina env configured")
		}

		client := New(cfg)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		resp, err := client.Classify(ctx,
			Classify("jina-embeddings-v3", []string{"billing", "support"},
				ClassifyInput{Text: "I need a refund"},
			),
		)
		if err != nil {
			t.Fatalf("classify failed: %v", err)
		}
		if resp == nil || len(resp.Data) == 0 {
			t.Fatalf("expected classify data, got: %#v", resp)
		}
	})

	t.Run("image", func(t *testing.T) {
		cfg, provider, model, ok := pickImageConfig()
		if !ok {
			t.Skip("no image provider env configured")
		}

		client := New(cfg)
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		resp, err := client.Image(ctx,
			Image(model, "a minimal line-art cat"),
			WithCount(1),
			WithImageProvider(provider),
		)
		if err != nil {
			t.Fatalf("image failed: %v", err)
		}
		if resp == nil || len(resp.Data) == 0 {
			t.Fatalf("expected image data, got: %#v", resp)
		}
		if strings.TrimSpace(resp.Data[0].B64JSON) == "" {
			t.Fatalf("expected base64 image data")
		}
	})

	t.Run("rerank", func(t *testing.T) {
		cfg, ok := pickJinaConfig()
		if !ok {
			t.Skip("no jina env configured")
		}

		client := New(cfg)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		resp, err := client.Rerank(ctx,
			Rerank("jina-reranker-v2-base-multilingual", "what is uniai?",
				RerankInput{Text: "a small Go client"},
				RerankInput{Text: "a javascript framework"},
			),
			WithTopN(2),
			WithReturnDocuments(true),
		)
		if err != nil {
			t.Fatalf("rerank failed: %v", err)
		}
		if resp == nil || len(resp.Results) == 0 {
			t.Fatalf("expected rerank results, got: %#v", resp)
		}
	})
}

func pickChatConfig() (Config, string, string, bool) {
	if key := env("TEST_OPENAI_CUSTOM_API_KEY"); key != "" {
		model := env("TEST_OPENAI_CUSTOM_MODEL")
		if model == "" {
			return Config{}, "", "", false
		}
		return Config{
			Provider:      "openai_custom",
			OpenAIAPIKey:  key,
			OpenAIAPIBase: env("TEST_OPENAI_CUSTOM_API_BASE"),
			OpenAIModel:   model,
		}, "openai_custom", model, true
	}

	if key := env("TEST_OPENAI_API_KEY"); key != "" {
		model := env("TEST_OPENAI_MODEL")
		if model == "" {
			return Config{}, "", "", false
		}
		return Config{
			Provider:      "openai",
			OpenAIAPIKey:  key,
			OpenAIAPIBase: env("TEST_OPENAI_API_BASE"),
			OpenAIModel:   model,
		}, "openai", model, true
	}

	if key := env("TEST_GEMINI_API_KEY"); key != "" {
		model := env("TEST_GEMINI_MODEL")
		if model == "" {
			return Config{}, "", "", false
		}
		return Config{
			Provider:      "gemini",
			OpenAIAPIKey:  key,
			OpenAIAPIBase: env("TEST_GEMINI_API_BASE"),
			OpenAIModel:   model,
		}, "gemini", model, true
	}

	if key := env("TEST_XAI_API_KEY"); key != "" {
		model := env("TEST_XAI_MODEL")
		if model == "" {
			return Config{}, "", "", false
		}
		return Config{
			Provider:     "xai",
			OpenAIAPIKey: key,
			OpenAIModel:  model,
		}, "xai", model, true
	}

	if key := env("TEST_DEEPSEEK_API_KEY"); key != "" {
		model := env("TEST_DEEPSEEK_MODEL")
		if model == "" {
			return Config{}, "", "", false
		}
		return Config{
			Provider:     "deepseek",
			OpenAIAPIKey: key,
			OpenAIModel:  model,
		}, "deepseek", model, true
	}

	if key := env("TEST_AZURE_API_KEY"); key != "" {
		endpoint := env("TEST_AZURE_ENDPOINT")
		model := env("TEST_AZURE_MODEL")
		if endpoint == "" || model == "" {
			return Config{}, "", "", false
		}
		return Config{
			Provider:            "azure",
			AzureOpenAIAPIKey:   key,
			AzureOpenAIEndpoint: endpoint,
			AzureOpenAIModel:    model,
		}, "azure", model, true
	}

	if key := env("TEST_ANTHROPIC_API_KEY"); key != "" {
		model := env("TEST_ANTHROPIC_MODEL")
		if model == "" {
			return Config{}, "", "", false
		}
		return Config{
			Provider:        "anthropic",
			AnthropicAPIKey: key,
			AnthropicModel:  model,
		}, "anthropic", model, true
	}

	if key := env("TEST_BEDROCK_AWS_KEY"); key != "" {
		secret := env("TEST_BEDROCK_AWS_SECRET")
		region := env("TEST_BEDROCK_AWS_REGION")
		arn := env("TEST_BEDROCK_MODEL_ARN")
		if secret == "" || arn == "" {
			return Config{}, "", "", false
		}
		return Config{
			Provider:           "bedrock",
			AwsKey:             key,
			AwsSecret:          secret,
			AwsRegion:          region,
			AwsBedrockModelArn: arn,
		}, "bedrock", arn, true
	}

	return Config{}, "", "", false
}

func pickEmbeddingConfig() (Config, string, string, bool) {
	if key := env("TEST_OPENAI_API_KEY"); key != "" {
		model := envDefault("TEST_OPENAI_EMBEDDING_MODEL", "text-embedding-3-small")
		return Config{
			OpenAIAPIKey:  key,
			OpenAIAPIBase: env("TEST_OPENAI_API_BASE"),
		}, "openai", model, true
	}

	if key := env("TEST_GEMINI_API_KEY"); key != "" {
		return Config{
			GeminiAPIKey:  key,
			GeminiAPIBase: env("TEST_GEMINI_API_BASE"),
		}, "gemini", "gemini-embedding-001", true
	}

	if key := env("TEST_JINA_API_KEY"); key != "" {
		model := envDefault("TEST_JINA_EMBEDDING_MODEL", "jina-embeddings-v3")
		return Config{
			JinaAPIKey:  key,
			JinaAPIBase: env("TEST_JINA_API_BASE"),
		}, "jina", model, true
	}

	return Config{}, "", "", false
}

func pickImageConfig() (Config, string, string, bool) {
	if key := env("TEST_OPENAI_API_KEY"); key != "" {
		model := envDefault("TEST_OPENAI_IMAGE_MODEL", "gpt-image-1")
		return Config{
			OpenAIAPIKey:  key,
			OpenAIAPIBase: env("TEST_OPENAI_API_BASE"),
		}, "openai", model, true
	}

	if key := env("TEST_OPENAI_CUSTOM_API_KEY"); key != "" {
		model := envDefault("TEST_OPENAI_CUSTOM_IMAGE_MODEL", "gpt-image-1")
		return Config{
			OpenAIAPIKey:  key,
			OpenAIAPIBase: env("TEST_OPENAI_CUSTOM_API_BASE"),
		}, "openai_custom", model, true
	}

	if key := env("TEST_GEMINI_API_KEY"); key != "" {
		model := envDefault("TEST_GEMINI_IMAGE_MODEL", "imagen-3.0-generate-002")
		return Config{
			GeminiAPIKey: key,
		}, "gemini", model, true
	}

	return Config{}, "", "", false
}

func pickJinaConfig() (Config, bool) {
	if key := env("TEST_JINA_API_KEY"); key != "" {
		return Config{
			JinaAPIKey:  key,
			JinaAPIBase: env("TEST_JINA_API_BASE"),
		}, true
	}
	return Config{}, false
}

func parseJSONObject(text string) (map[string]any, error) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start == -1 || end == -1 || end < start {
		return nil, fmt.Errorf("no JSON object found")
	}

	candidate := text[start : end+1]
	var out map[string]any
	if err := json.Unmarshal([]byte(candidate), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func env(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

func envDefault(key, def string) string {
	if val := env(key); val != "" {
		return val
	}
	return def
}
