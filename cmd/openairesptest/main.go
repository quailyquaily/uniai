package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai"
)

const (
	defaultModel         = "gpt-5.4"
	defaultTimeoutSecond = 120
	defaultPrompt        = "Use the tool to get the weather for Tokyo, then answer in one short sentence."
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("openairesptest", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	model := fs.String("model", envOrDefault("OPENAI_MODEL", defaultModel), "OpenAI model")
	prompt := fs.String("prompt", envOrDefault("PROMPT", defaultPrompt), "user prompt")
	timeoutSec := fs.Int("timeout", envIntOrDefault("TIMEOUT", defaultTimeoutSecond), "timeout in seconds")
	skipLegacy := fs.Bool("skip-openai", false, "skip the legacy Chat Completions check")
	skipResponses := fs.Bool("skip-openai-resp", false, "skip the Responses API run")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("usage: openairesptest [--model model] [--prompt text] [--timeout sec] [--skip-openai] [--skip-openai-resp]")
	}

	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return fmt.Errorf("missing required env OPENAI_API_KEY")
	}
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_API_BASE"))
	modelName := strings.TrimSpace(*model)
	if modelName == "" {
		return fmt.Errorf("model is required")
	}

	client := uniai.New(uniai.Config{
		OpenAIAPIKey:  apiKey,
		OpenAIAPIBase: baseURL,
		OpenAIModel:   modelName,
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSec)*time.Second)
	defer cancel()

	fmt.Printf("model=%s\n", modelName)
	if baseURL != "" {
		fmt.Printf("base=%s\n", baseURL)
	}

	if !*skipLegacy {
		if err := runLegacyCheck(ctx, client, modelName, *prompt); err != nil {
			return err
		}
	}
	if !*skipResponses {
		if err := runResponsesFlow(ctx, client, modelName, *prompt); err != nil {
			return err
		}
	}

	return nil
}

func runLegacyCheck(ctx context.Context, client *uniai.Client, model string, prompt string) error {
	fmt.Println("legacy=openai")

	_, err := client.Chat(ctx,
		uniai.WithProvider("openai"),
		uniai.WithModel(model),
		uniai.WithMessages(
			uniai.System("You are a precise assistant. Use tools when required."),
			uniai.User(prompt),
		),
		uniai.WithTools(testTools()),
		uniai.WithToolChoice(uniai.ToolChoiceRequired()),
		uniai.WithReasoningEffort(uniai.ReasoningEffortHigh),
	)
	if err == nil {
		return fmt.Errorf("expected openai provider to fail for gpt-5.4 + reasoning_effort + function tools, but it succeeded")
	}

	msg := err.Error()
	fmt.Printf("legacy_error=%s\n", msg)
	if strings.Contains(msg, "Function tools with reasoning_effort are not supported") &&
		strings.Contains(msg, "/v1/chat/completions") &&
		strings.Contains(msg, "/v1/responses") {
		fmt.Println("legacy_result=expected_failure")
		return nil
	}

	return fmt.Errorf("openai provider failed, but not with the expected chat/completions reasoning+tools error: %w", err)
}

func runResponsesFlow(ctx context.Context, client *uniai.Client, model string, prompt string) error {
	fmt.Println("responses=openai_resp")

	resp, err := client.Chat(ctx,
		uniai.WithProvider("openai_resp"),
		uniai.WithModel(model),
		uniai.WithMessages(
			uniai.System("You are a precise assistant. Use tools when required."),
			uniai.User(prompt),
		),
		uniai.WithTools(testTools()),
		uniai.WithToolChoice(uniai.ToolChoiceRequired()),
		uniai.WithReasoningEffort(uniai.ReasoningEffortHigh),
		uniai.WithReasoningDetails(),
	)
	if err != nil {
		return fmt.Errorf("openai_resp first turn failed: %w", err)
	}

	fmt.Printf("responses_id=%s\n", strings.TrimSpace(resp.ID))
	fmt.Printf("responses_tool_calls=%d\n", len(resp.ToolCalls))
	if resp.Reasoning != nil && len(resp.Reasoning.Summary) > 0 {
		fmt.Printf("responses_reasoning_summary=%s\n", strings.Join(resp.Reasoning.Summary, " | "))
	}

	if len(resp.ToolCalls) == 0 {
		fmt.Printf("responses_text=%s\n", strings.TrimSpace(resp.Text))
		return nil
	}

	var last *uniai.ChatResult
	prevID := strings.TrimSpace(resp.ID)
	current := resp

	for round := 0; round < 4; round++ {
		last = current

		toolOutputs := make([]uniai.Message, 0, len(current.ToolCalls))
		for _, call := range current.ToolCalls {
			payload, err := executeTool(call)
			if err != nil {
				return fmt.Errorf("tool execution failed for %s: %w", call.Function.Name, err)
			}
			toolOutputs = append(toolOutputs, uniai.ToolResult(call.ID, payload))
			fmt.Printf("tool_result[%s]=%s\n", call.ID, payload)
		}

		next, err := client.Chat(ctx,
			uniai.WithProvider("openai_resp"),
			uniai.WithModel(model),
			uniai.WithMessages(toolOutputs...),
			uniai.WithReasoningEffort(uniai.ReasoningEffortHigh),
			uniai.WithReasoningDetails(),
			uniai.WithOpenAIOptions(structs.JSONMap{
				"previous_response_id": prevID,
			}),
		)
		if err != nil {
			return fmt.Errorf("openai_resp follow-up failed: %w", err)
		}

		fmt.Printf("responses_followup_id=%s\n", strings.TrimSpace(next.ID))
		if next.Reasoning != nil && len(next.Reasoning.Summary) > 0 {
			fmt.Printf("responses_followup_reasoning_summary=%s\n", strings.Join(next.Reasoning.Summary, " | "))
		}
		if len(next.ToolCalls) == 0 {
			fmt.Printf("responses_text=%s\n", strings.TrimSpace(next.Text))
			return nil
		}

		prevID = strings.TrimSpace(next.ID)
		current = next
	}

	if last != nil {
		return fmt.Errorf("openai_resp did not reach a final non-tool response after multiple rounds; last tool_calls=%d", len(last.ToolCalls))
	}
	return fmt.Errorf("openai_resp did not produce a final response")
}

func testTools() []uniai.Tool {
	return []uniai.Tool{
		uniai.FunctionTool("get_weather", "Get current weather for a city", []byte(`{
			"type": "object",
			"properties": {
				"city": { "type": "string" }
			},
			"required": ["city"]
		}`)),
	}
}

func executeTool(call uniai.ToolCall) (string, error) {
	switch strings.TrimSpace(call.Function.Name) {
	case "get_weather":
		var in struct {
			City string `json:"city"`
		}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &in); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		if strings.TrimSpace(in.City) == "" {
			in.City = "unknown"
		}
		out := map[string]any{
			"city":        in.City,
			"temperature": "19C",
			"condition":   "clear",
		}
		data, err := json.Marshal(out)
		if err != nil {
			return "", err
		}
		return string(data), nil
	default:
		return "", fmt.Errorf("unsupported tool %q", call.Function.Name)
	}
}

func envOrDefault(key string, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}

func envIntOrDefault(key string, fallback int) int {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}
	n, err := strconv.Atoi(val)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
