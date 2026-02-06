package uniai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestChatEchoJSON(t *testing.T) {
	configs := pickChatConfigs()
	if len(configs) == 0 {
		t.Skip("no chat provider env configured")
	}

	payload := `{"ok":true,"echo":"ping"}`

	for _, tc := range configs {
		tc := tc
		t.Run(tc.provider, func(t *testing.T) {
			client := New(tc.cfg)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			t.Logf("chat provider=%s model=%s", tc.provider, tc.model)
			resp, err := client.Chat(ctx,
				WithProvider(tc.provider),
				WithModel(tc.model),
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
			t.Logf("chat response length=%d", len(resp.Text))

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
		})
	}
}

func TestChatToolCalling(t *testing.T) {
	configs := pickChatConfigs()
	if len(configs) == 0 {
		t.Skip("no chat provider env configured")
	}

	toolSchema := []byte(`{
		"type": "object",
		"properties": { "city": { "type": "string" } },
		"required": ["city"]
	}`)

	for _, tc := range configs {
		tc := tc
		t.Run(tc.provider, func(t *testing.T) {
			client := New(tc.cfg)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			resp, err := client.Chat(ctx,
				WithProvider(tc.provider),
				WithModel(tc.model),
				WithMessages(
					System("You must call the tool. Do not answer with text."),
					User("What's the weather in Tokyo?"),
				),
				WithTools([]Tool{
					FunctionTool("get_weather", "Get current weather", toolSchema),
				}),
				WithToolChoice(ToolChoiceFunction("get_weather")),
				WithToolsEmulationMode(ToolsEmulationFallback),
				WithTemperature(0),
			)

			fmt.Printf("\n\n%s - Response: %+v\n\n", tc.provider, resp)

			if err != nil {
				t.Fatalf("chat failed: %v", err)
			}
			if resp == nil || len(resp.ToolCalls) == 0 {
				t.Fatalf("expected tool calls, got: %#v", resp)
			}
			if resp.ToolCalls[0].Function.Name != "get_weather" {
				t.Fatalf("unexpected tool name: %s", resp.ToolCalls[0].Function.Name)
			}
			var args map[string]any
			if err := json.Unmarshal([]byte(resp.ToolCalls[0].Function.Arguments), &args); err != nil {
				t.Fatalf("invalid tool arguments: %v", err)
			}
			if city, ok := args["city"].(string); !ok || strings.TrimSpace(city) == "" {
				t.Fatalf("expected city argument, got: %#v", args["city"])
			}
		})
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

	t.Run("audio", func(t *testing.T) {
		cfg, provider, model, audioData, ok, err := pickAudioConfig()
		if err != nil {
			t.Fatalf("audio config failed: %v", err)
		}
		if !ok {
			t.Skip("no audio provider env configured")
		}

		client := New(cfg)
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		resp, err := client.Audio(ctx,
			Audio(model, audioData),
			WithAudioProvider(provider),
		)
		if err != nil {
			t.Fatalf("audio failed: %v", err)
		}
		fmt.Printf("\n\n%s - Audio Text: %s\n\n", provider, resp.Text)
		if resp == nil || strings.TrimSpace(resp.Text) == "" {
			t.Fatalf("expected audio transcription text, got: %#v", resp)
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

type chatConfig struct {
	provider string
	model    string
	cfg      Config
}

func pickChatConfigs() []chatConfig {
	var out []chatConfig
	if key := env("TEST_OPENAI_CUSTOM_API_KEY"); key != "" {
		model := env("TEST_OPENAI_CUSTOM_MODEL")
		if model != "" {
			out = append(out, chatConfig{
				provider: "openai_custom",
				model:    model,
				cfg: Config{
					Provider:      "openai_custom",
					OpenAIAPIKey:  key,
					OpenAIAPIBase: env("TEST_OPENAI_CUSTOM_API_BASE"),
					OpenAIModel:   model,
				},
			})
		}
	}

	if key := env("TEST_OPENAI_API_KEY"); key != "" {
		model := env("TEST_OPENAI_MODEL")
		if model != "" {
			out = append(out, chatConfig{
				provider: "openai",
				model:    model,
				cfg: Config{
					Provider:      "openai",
					OpenAIAPIKey:  key,
					OpenAIAPIBase: env("TEST_OPENAI_API_BASE"),
					OpenAIModel:   model,
				},
			})
		}
	}

	if key := env("TEST_GEMINI_API_KEY"); key != "" {
		model := env("TEST_GEMINI_MODEL")
		if model != "" {
			out = append(out, chatConfig{
				provider: "gemini",
				model:    model,
				cfg: Config{
					Provider:      "gemini",
					GeminiAPIKey:  key,
					GeminiAPIBase: env("TEST_GEMINI_API_BASE"),
					OpenAIModel:   model,
				},
			})
		}
	}

	if key := env("TEST_XAI_API_KEY"); key != "" {
		model := env("TEST_XAI_MODEL")
		if model != "" {
			out = append(out, chatConfig{
				provider: "xai",
				model:    model,
				cfg: Config{
					Provider:     "xai",
					OpenAIAPIKey: key,
					OpenAIModel:  model,
				},
			})
		}
	}

	if key := env("TEST_DEEPSEEK_API_KEY"); key != "" {
		model := env("TEST_DEEPSEEK_MODEL")
		if model != "" {
			out = append(out, chatConfig{
				provider: "deepseek",
				model:    model,
				cfg: Config{
					Provider:     "deepseek",
					OpenAIAPIKey: key,
					OpenAIModel:  model,
				},
			})
		}
	}

	if key := env("TEST_AZURE_API_KEY"); key != "" {
		endpoint := env("TEST_AZURE_ENDPOINT")
		model := env("TEST_AZURE_MODEL")
		if endpoint != "" && model != "" {
			out = append(out, chatConfig{
				provider: "azure",
				model:    model,
				cfg: Config{
					Provider:            "azure",
					AzureOpenAIAPIKey:   key,
					AzureOpenAIEndpoint: endpoint,
					AzureOpenAIModel:    model,
				},
			})
		}
	}

	if key := env("TEST_ANTHROPIC_API_KEY"); key != "" {
		model := env("TEST_ANTHROPIC_MODEL")
		if model != "" {
			out = append(out, chatConfig{
				provider: "anthropic",
				model:    model,
				cfg: Config{
					Provider:        "anthropic",
					AnthropicAPIKey: key,
					AnthropicModel:  model,
				},
			})
		}
	}

	if key := env("TEST_BEDROCK_AWS_KEY"); key != "" {
		secret := env("TEST_BEDROCK_AWS_SECRET")
		region := env("TEST_BEDROCK_AWS_REGION")
		arn := env("TEST_BEDROCK_MODEL_ARN")
		if secret != "" && arn != "" {
			out = append(out, chatConfig{
				provider: "bedrock",
				model:    arn,
				cfg: Config{
					Provider:           "bedrock",
					AwsKey:             key,
					AwsSecret:          secret,
					AwsRegion:          region,
					AwsBedrockModelArn: arn,
				},
			})
		}
	}

	if accountID := env("TEST_CLOUDFLARE_ACCOUNT_ID"); accountID != "" {
		token := env("TEST_CLOUDFLARE_API_TOKEN")
		model := env("TEST_CLOUDFLARE_TEXT_MODEL")
		if token != "" && model != "" {
			out = append(out, chatConfig{
				provider: "cloudflare",
				model:    model,
				cfg: Config{
					Provider:            "cloudflare",
					CloudflareAccountID: accountID,
					CloudflareAPIToken:  token,
					CloudflareAPIBase:   env("TEST_CLOUDFLARE_API_BASE"),
				},
			})
		}
	}

	return out
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

func pickAudioConfig() (Config, string, string, string, bool, error) {
	accountID := env("TEST_CLOUDFLARE_ACCOUNT_ID")
	token := env("TEST_CLOUDFLARE_API_TOKEN")
	model := env("TEST_CLOUDFLARE_AUDIO_MODEL")
	audioPath := env("TEST_CLOUDFLARE_AUDIO_FILEPATH")
	if accountID == "" || token == "" || model == "" || audioPath == "" {
		return Config{}, "", "", "", false, nil
	}
	data, err := os.ReadFile(audioPath)
	if err != nil {
		return Config{}, "", "", "", false, fmt.Errorf("read audio file: %w", err)
	}
	audioData := base64.StdEncoding.EncodeToString(data)
	return Config{
		CloudflareAccountID: accountID,
		CloudflareAPIToken:  token,
		CloudflareAPIBase:   env("TEST_CLOUDFLARE_API_BASE"),
	}, "cloudflare", model, audioData, true, nil
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
	var out map[string]any
	for _, candidate := range findJSONSnippets(text) {
		if err := json.Unmarshal([]byte(candidate), &out); err == nil {
			return out, nil
		}
	}

	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start == -1 || end == -1 || end < start {
		return nil, fmt.Errorf("no JSON object found")
	}

	candidate := text[start : end+1]
	if err := json.Unmarshal([]byte(candidate), &out); err == nil {
		return out, nil
	}
	if unescaped := tryUnescapeJSON(candidate); unescaped != "" {
		if err := json.Unmarshal([]byte(unescaped), &out); err == nil {
			return out, nil
		}
	}
	return nil, fmt.Errorf("no valid JSON object found")
}

func tryUnescapeJSON(raw string) string {
	if !strings.Contains(raw, `\"`) && !strings.Contains(raw, `\\\"`) {
		return ""
	}
	escaped := strings.ReplaceAll(raw, "\n", "\\n")
	escaped = strings.ReplaceAll(escaped, "\t", "\\t")
	escaped = strings.ReplaceAll(escaped, "\r", "\\r")
	unquoted, err := strconv.Unquote("\"" + escaped + "\"")
	if err != nil {
		return ""
	}
	return unquoted
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
