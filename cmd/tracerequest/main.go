package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quailyquaily/uniai"
)

const (
	defaultProvider      = "openai"
	defaultScene         = sceneNone
	defaultPrompt        = "Reply with exactly: tracerequest-demo"
	defaultTimeoutSecond = 90
	defaultDumpDir       = "dump"
	toolCallingMaxRounds = 6
	toolCallingMinRounds = 2

	dumpFileTimeLayout = "2006-01-02_15-04-05"
	dumpLineTimeLayout = "2006-01-02 15:04:05"

	sceneNone        = "none"
	sceneToolCalling = "toolcalling"
)

type traceEntry struct {
	Label   string
	Payload string
	At      time.Time
}

type traceRecorder struct {
	mu      sync.Mutex
	entries []traceEntry
}

func (r *traceRecorder) DebugFn(label, payload string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.entries = append(r.entries, traceEntry{
		Label:   label,
		Payload: payload,
		At:      time.Now(),
	})
}

func (r *traceRecorder) Entries() []traceEntry {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]traceEntry, len(r.entries))
	copy(out, r.entries)
	return out
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	timeoutDefault, err := timeoutFromEnv()
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("tracerequest", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	provider := fs.String("provider", envOrDefault("PROVIDER", defaultProvider), "provider: openai|cloudflare|gemini")
	scene := fs.String("scene", envOrDefault("SCENE", defaultScene), "scene: none|toolcalling")
	model := fs.String("model", strings.TrimSpace(os.Getenv("MODEL")), "model/deployment/model-arn (provider specific)")
	prompt := fs.String("prompt", envOrDefault("PROMPT", defaultPrompt), "chat prompt")
	timeoutSec := fs.Int("timeout", timeoutDefault, "request timeout in seconds")
	dumpDir := fs.String("dump-dir", envOrDefault("DUMP_DIR", defaultDumpDir), "trace output directory")

	if err := fs.Parse(args); err != nil {
		return err
	}

	rest := fs.Args()
	if len(rest) > 1 {
		return usageError()
	}
	if len(rest) == 1 && rest[0] != "run" {
		return usageError()
	}

	if *timeoutSec <= 0 {
		return fmt.Errorf("timeout must be > 0")
	}
	selectedScene, err := normalizeScene(*scene)
	if err != nil {
		return err
	}

	userPrompt := strings.TrimSpace(*prompt)
	if userPrompt == "" {
		return fmt.Errorf("prompt is required (set --prompt or PROMPT)")
	}

	cfg, requestModel, err := buildClientConfig(*provider, *model)
	if err != nil {
		return err
	}

	recorder := &traceRecorder{}
	client := uniai.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSec)*time.Second)
	defer cancel()

	resp, chatErr := runSceneChat(ctx, client, selectedScene, requestModel, userPrompt, recorder.DebugFn)

	entries := recorder.Entries()
	if len(entries) == 0 {
		entries = append(entries, traceEntry{
			Label:   "trace.empty",
			Payload: "no request/response payload captured",
			At:      time.Now(),
		})
	}

	dumpPath, dumpErr := writeDumpFile(strings.TrimSpace(*dumpDir), entries)
	if dumpErr != nil {
		if chatErr != nil {
			return fmt.Errorf("chat failed: %v; write dump failed: %w", chatErr, dumpErr)
		}
		return dumpErr
	}

	fmt.Printf("provider=%s\n", cfg.Provider)
	fmt.Printf("scene=%s\n", selectedScene)
	if strings.TrimSpace(requestModel) != "" {
		fmt.Printf("model=%s\n", requestModel)
	}
	fmt.Printf("dump=%s\n", dumpPath)

	if chatErr != nil {
		return fmt.Errorf("chat failed: %w", chatErr)
	}
	if resp != nil {
		fmt.Printf("response=%s\n", strings.TrimSpace(resp.Text))
	}

	return nil
}

func runSceneChat(
	ctx context.Context,
	client *uniai.Client,
	scene string,
	model string,
	prompt string,
	debugFn uniai.DebugFn,
) (*uniai.ChatResult, error) {
	switch scene {
	case sceneToolCalling:
		return runToolCallingScene(ctx, client, model, prompt, debugFn)
	default:
		return runBasicScene(ctx, client, model, prompt, debugFn)
	}
}

func runBasicScene(
	ctx context.Context,
	client *uniai.Client,
	model string,
	prompt string,
	debugFn uniai.DebugFn,
) (*uniai.ChatResult, error) {
	opts := []uniai.ChatOption{
		uniai.WithDebugFn(debugFn),
		uniai.WithMessages(uniai.User(prompt)),
	}
	if strings.TrimSpace(model) != "" {
		opts = append(opts, uniai.WithModel(model))
	}
	return client.Chat(ctx, opts...)
}

func runToolCallingScene(
	ctx context.Context,
	client *uniai.Client,
	model string,
	prompt string,
	debugFn uniai.DebugFn,
) (*uniai.ChatResult, error) {
	tools := mockToolCallingTools()
	messages := []uniai.Message{
		uniai.System(
			"You are a tool-calling assistant. Use tools to complete the request. " +
				"You must call tools in at least two assistant turns before giving a final answer.",
		),
		uniai.User(prompt),
	}

	var lastResp *uniai.ChatResult
	for round := 1; round <= toolCallingMaxRounds; round++ {
		choice := uniai.ToolChoiceAuto()
		if round <= toolCallingMinRounds {
			choice = uniai.ToolChoiceRequired()
		}

		opts := []uniai.ChatOption{
			uniai.WithDebugFn(debugFn),
			uniai.WithReplaceMessages(messages...),
			uniai.WithTools(tools),
			uniai.WithToolChoice(choice),
		}
		if strings.TrimSpace(model) != "" {
			opts = append(opts, uniai.WithModel(model))
		}

		resp, err := client.Chat(ctx, opts...)
		if err != nil {
			return nil, err
		}
		lastResp = resp
		if resp == nil || len(resp.ToolCalls) == 0 {
			if round <= toolCallingMinRounds {
				return nil, fmt.Errorf("toolcalling scene expected tool_calls in round %d, got none", round)
			}
			return resp, nil
		}

		messages = appendToolRoundMessages(messages, resp)
	}

	return lastResp, nil
}

func appendToolRoundMessages(messages []uniai.Message, resp *uniai.ChatResult) []uniai.Message {
	if resp == nil || len(resp.ToolCalls) == 0 {
		return messages
	}

	toolCalls := normalizeToolCalls(resp.ToolCalls)
	assistantMsg := uniai.Message{
		Role:      uniai.RoleAssistant,
		Content:   resp.Text,
		ToolCalls: toolCalls,
	}
	messages = append(messages, assistantMsg)

	for _, call := range toolCalls {
		messages = append(messages, uniai.ToolResult(call.ID, mockToolResult(call)))
	}
	return messages
}

func normalizeToolCalls(calls []uniai.ToolCall) []uniai.ToolCall {
	out := make([]uniai.ToolCall, 0, len(calls))
	for i, call := range calls {
		if strings.TrimSpace(call.ID) == "" {
			call.ID = fmt.Sprintf("toolcall_%d", i+1)
		}
		out = append(out, call)
	}
	return out
}

func mockToolResult(call uniai.ToolCall) string {
	args := parseToolArguments(call.Function.Arguments)
	name := strings.TrimSpace(call.Function.Name)

	switch name {
	case "get_weather":
		location := lookupString(args, "location", "unknown")
		return mustJSON(map[string]any{
			"ok":           true,
			"tool":         name,
			"location":     location,
			"weather":      "sunny",
			"temperatureC": 23,
		})
	case "get_direction":
		from := lookupString(args, "from", "origin")
		to := lookupString(args, "to", "destination")
		return mustJSON(map[string]any{
			"ok":       true,
			"tool":     name,
			"from":     from,
			"to":       to,
			"distance": "6.1km",
			"duration": "24m",
		})
	case "send_message":
		to := lookupString(args, "to", "unknown")
		message := lookupString(args, "message", "")
		return mustJSON(map[string]any{
			"ok":      true,
			"tool":    name,
			"to":      to,
			"message": message,
			"status":  "sent",
		})
	default:
		return mustJSON(map[string]any{
			"ok":       true,
			"tool":     name,
			"echoArgs": args,
		})
	}
}

func parseToolArguments(raw string) map[string]any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]any{}
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
		return map[string]any{
			"_raw": raw,
		}
	}
	return args
}

func lookupString(values map[string]any, key, fallback string) string {
	if values == nil {
		return fallback
	}
	v, ok := values[key]
	if !ok {
		return fallback
	}
	s, ok := v.(string)
	if !ok {
		return fallback
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	return s
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"ok":true}`
	}
	return string(b)
}

func usageError() error {
	return fmt.Errorf("usage: tracerequest [--provider name] [--scene none|toolcalling] [--model model] [--prompt text] [--timeout seconds] [--dump-dir dump] [run]")
}

func timeoutFromEnv() (int, error) {
	raw := strings.TrimSpace(os.Getenv("TIMEOUT_SECONDS"))
	if raw == "" {
		return defaultTimeoutSecond, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("invalid TIMEOUT_SECONDS %q: must be a positive integer", raw)
	}
	return v, nil
}

func buildClientConfig(providerRaw, modelRaw string) (uniai.Config, string, error) {
	provider := strings.ToLower(strings.TrimSpace(providerRaw))
	if provider == "" {
		provider = defaultProvider
	}
	modelArg := strings.TrimSpace(modelRaw)

	cfg := uniai.Config{Provider: provider}

	switch provider {
	case "cloudflare":
		accountID, err := requireAnyEnv("CLOUDFLARE_ACCOUNT_ID")
		if err != nil {
			return uniai.Config{}, "", fmt.Errorf("cloudflare provider: %w", err)
		}
		apiToken, err := requireAnyEnv("CLOUDFLARE_API_TOKEN")
		if err != nil {
			return uniai.Config{}, "", fmt.Errorf("cloudflare provider: %w", err)
		}
		model, err := requireModel(modelArg, "CLOUDFLARE_MODEL")
		if err != nil {
			return uniai.Config{}, "", fmt.Errorf("cloudflare provider: %w", err)
		}
		cfg.CloudflareAccountID = accountID
		cfg.CloudflareAPIToken = apiToken
		cfg.CloudflareAPIBase = envAny("CLOUDFLARE_API_BASE")
		return cfg, model, nil

	case "gemini":
		apiKey, err := requireAnyEnv("GEMINI_API_KEY")
		if err != nil {
			return uniai.Config{}, "", fmt.Errorf("gemini provider: %w", err)
		}
		model, err := requireModel(modelArg, "GEMINI_MODEL")
		if err != nil {
			return uniai.Config{}, "", fmt.Errorf("gemini provider: %w", err)
		}
		cfg.GeminiAPIKey = apiKey
		cfg.GeminiAPIBase = envAny("GEMINI_API_BASE")
		cfg.GeminiModel = model
		return cfg, model, nil

	case "openai":
		apiKey, err := requireAnyEnv("OPENAI_API_KEY")
		if err != nil {
			return uniai.Config{}, "", fmt.Errorf("openai provider: %w", err)
		}
		model, err := requireModel(modelArg, "OPENAI_MODEL")
		if err != nil {
			return uniai.Config{}, "", fmt.Errorf("openai provider: %w", err)
		}
		cfg.OpenAIAPIKey = apiKey
		cfg.OpenAIAPIBase = envAny("OPENAI_API_BASE")
		cfg.OpenAIModel = model
		return cfg, model, nil

	default:
		return uniai.Config{}, "", fmt.Errorf("unsupported provider %q (supported: openai, cloudflare, gemini)", provider)
	}
}

func writeDumpFile(dir string, entries []traceEntry) (string, error) {
	if strings.TrimSpace(dir) == "" {
		dir = defaultDumpDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create dump dir %q: %w", dir, err)
	}

	name := dumpFileName(time.Now())
	path := filepath.Join(dir, name)
	content := formatDump(entries)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write dump file %q: %w", path, err)
	}

	if abs, err := filepath.Abs(path); err == nil {
		return abs, nil
	}
	return path, nil
}

func dumpFileName(now time.Time) string {
	return now.Format(dumpFileTimeLayout) + ".md"
}

func normalizeScene(raw string) (string, error) {
	scene := strings.ToLower(strings.TrimSpace(raw))
	switch scene {
	case "", sceneNone:
		return sceneNone, nil
	case sceneToolCalling:
		return sceneToolCalling, nil
	default:
		return "", fmt.Errorf("unsupported scene %q (supported: %s, %s)", raw, sceneNone, sceneToolCalling)
	}
}

func mockToolCallingTools() []uniai.Tool {
	return []uniai.Tool{
		uniai.FunctionTool(
			"get_weather",
			"Get weather for a city or place.",
			[]byte(`{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}`),
		),
		uniai.FunctionTool(
			"get_direction",
			"Get route direction from one place to another place.",
			[]byte(`{"type":"object","properties":{"from":{"type":"string"},"to":{"type":"string"}},"required":["from","to"]}`),
		),
		uniai.FunctionTool(
			"send_message",
			"Send a message to a recipient.",
			[]byte(`{"type":"object","properties":{"to":{"type":"string"},"message":{"type":"string"}},"required":["to","message"]}`),
		),
	}
}

func formatDump(entries []traceEntry) string {
	var b strings.Builder

	for i, e := range entries {
		fmt.Fprintf(&b, "## %q\n", e.Label)
		fmt.Fprintf(&b, "* time: %s\n", e.At.Format(dumpLineTimeLayout))
		b.WriteString("* payload: |\n")
		payload := prettyJSONPayload(e.Payload)
		b.WriteString(payload)
		if !strings.HasSuffix(payload, "\n") {
			b.WriteByte('\n')
		}
		if i < len(entries)-1 {
			b.WriteByte('\n')
		}
	}

	return b.String()
}

func prettyJSONPayload(payload string) string {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return payload
	}

	var out bytes.Buffer
	if err := json.Indent(&out, []byte(trimmed), "", "  "); err != nil {
		return payload
	}
	return out.String()
}

func requireModel(flagModel string, envNames ...string) (string, error) {
	names := make([]string, 0, len(envNames)+1)
	names = append(names, "MODEL")
	names = append(names, envNames...)
	model := firstNonEmpty(strings.TrimSpace(flagModel), envAny(names...))
	if model == "" {
		return "", fmt.Errorf("model is required (use --model or one of %s)", strings.Join(names, ", "))
	}
	return model, nil
}

func requireAnyEnv(names ...string) (string, error) {
	if v := envAny(names...); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("missing required env: one of %s", strings.Join(names, ", "))
}

func envAny(names ...string) string {
	for _, name := range names {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	return ""
}

func envOrDefault(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
