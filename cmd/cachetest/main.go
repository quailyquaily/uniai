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
	defaultProvider      = "openai_resp"
	defaultScene         = sceneStats
	defaultTimeoutSecond = 180
	defaultTTL           = "5m"
	defaultAttempts      = 3
	defaultMaxTokens     = 64
)

const (
	sceneStats  = "stats"
	sceneScope  = "scope"
	sceneStream = "stream"
)

type runSummary struct {
	Provider    string       `json:"provider"`
	Model       string       `json:"model"`
	Scene       string       `json:"scene"`
	Success     bool         `json:"success"`
	Attempts    int          `json:"attempts"`
	Blocking    bool         `json:"blocking"`
	Streaming   bool         `json:"streaming"`
	TTL         string       `json:"ttl,omitempty"`
	UsageWarm   *uniai.Usage `json:"usage_warm,omitempty"`
	UsageHit    *uniai.Usage `json:"usage_hit,omitempty"`
	UsageMiss   *uniai.Usage `json:"usage_miss,omitempty"`
	UsageStream *uniai.Usage `json:"usage_stream,omitempty"`
	Error       string       `json:"error,omitempty"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("cachetest", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	provider := fs.String("provider", envOrDefault("PROVIDER", defaultProvider), "provider: openai|openai_resp|azure|anthropic|bedrock")
	scene := fs.String("scene", envOrDefault("CACHE_SCENE", defaultScene), "scene: stats|scope|stream")
	model := fs.String("model", strings.TrimSpace(os.Getenv("MODEL")), "provider model/deployment/model arn")
	ttl := fs.String("ttl", envOrDefault("CACHE_TTL", defaultTTL), "cache ttl for scoped cache providers: 5m|1h")
	timeoutSec := fs.Int("timeout", envIntOrDefault("TIMEOUT_SECONDS", defaultTimeoutSecond), "timeout in seconds")
	streamFlag := fs.Bool("stream", envBool("CACHE_STREAM"), "alias for scene=stream")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("usage: cachetest [--provider name] [--scene stats|scope|stream] [--model model] [--ttl 5m|1h] [--timeout seconds] [--stream]")
	}
	if *timeoutSec <= 0 {
		return fmt.Errorf("timeout must be > 0")
	}

	selectedScene, err := normalizeScene(*scene, *streamFlag)
	if err != nil {
		return err
	}
	selectedProvider := strings.ToLower(strings.TrimSpace(*provider))
	if selectedProvider == "" {
		selectedProvider = defaultProvider
	}

	cfg, requestModel, err := buildClientConfig(selectedProvider, *model)
	if err != nil {
		return err
	}

	client := uniai.New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSec)*time.Second)
	defer cancel()

	summary, runErr := runScene(ctx, client, selectedProvider, requestModel, selectedScene, strings.TrimSpace(*ttl))
	if summary.Provider == "" {
		summary.Provider = selectedProvider
	}
	if summary.Model == "" {
		summary.Model = requestModel
	}
	if summary.Scene == "" {
		summary.Scene = selectedScene
	}
	if runErr != nil {
		summary.Error = runErr.Error()
	}

	printSummary(summary)
	if runErr != nil {
		return runErr
	}
	return nil
}

func runScene(ctx context.Context, client *uniai.Client, provider, model, scene, ttl string) (runSummary, error) {
	switch provider {
	case "openai", "openai_resp", "azure", "anthropic", "bedrock":
	default:
		return runSummary{}, fmt.Errorf("unsupported provider %q (supported: openai, openai_resp, azure, anthropic, bedrock)", provider)
	}

	switch scene {
	case sceneStats:
		return runStatsScene(ctx, client, provider, model)
	case sceneScope:
		return runScopeScene(ctx, client, provider, model, ttl)
	case sceneStream:
		return runStreamScene(ctx, client, provider, model)
	default:
		return runSummary{}, fmt.Errorf("unsupported scene %q", scene)
	}
}

func runStatsScene(ctx context.Context, client *uniai.Client, provider, model string) (runSummary, error) {
	summary := runSummary{
		Provider:  provider,
		Model:     model,
		Scene:     sceneStats,
		Blocking:  true,
		Streaming: false,
	}

	for attempt := 1; attempt <= defaultAttempts; attempt++ {
		warm, _, err := executeChat(ctx, client, statsOptions(provider, model), false)
		if err != nil {
			summary.Attempts = attempt
			return summary, err
		}
		hit, _, err := executeChat(ctx, client, statsOptions(provider, model), false)
		if err != nil {
			summary.Attempts = attempt
			return summary, err
		}

		summary.Attempts = attempt
		summary.UsageWarm = &warm.Usage
		summary.UsageHit = &hit.Usage
		if hit.Usage.Cache.CachedInputTokens > 0 {
			summary.Success = true
			return summary, nil
		}
	}

	return summary, fmt.Errorf("no cache hit observed after %d attempts", summary.Attempts)
}

func runStreamScene(ctx context.Context, client *uniai.Client, provider, model string) (runSummary, error) {
	if !providerSupportsStreaming(provider) {
		return runSummary{
			Provider: provider,
			Model:    model,
			Scene:    sceneStream,
		}, fmt.Errorf("provider %q does not support streaming validation in cachetest", provider)
	}

	summary := runSummary{
		Provider:  provider,
		Model:     model,
		Scene:     sceneStream,
		Blocking:  true,
		Streaming: true,
	}

	for attempt := 1; attempt <= defaultAttempts; attempt++ {
		warm, _, err := executeChat(ctx, client, statsOptions(provider, model), false)
		if err != nil {
			summary.Attempts = attempt
			return summary, err
		}
		hit, _, err := executeChat(ctx, client, statsOptions(provider, model), false)
		if err != nil {
			summary.Attempts = attempt
			return summary, err
		}
		streamed, streamUsage, err := executeChat(ctx, client, statsOptions(provider, model), true)
		if err != nil {
			summary.Attempts = attempt
			return summary, err
		}

		summary.Attempts = attempt
		summary.UsageWarm = &warm.Usage
		summary.UsageHit = &hit.Usage
		summary.UsageStream = streamUsage
		if summary.UsageStream == nil {
			summary.UsageStream = &streamed.Usage
		}
		if hit.Usage.Cache.CachedInputTokens > 0 &&
			summary.UsageStream != nil &&
			summary.UsageStream.Cache.CachedInputTokens > 0 {
			summary.Success = true
			return summary, nil
		}
	}

	return summary, fmt.Errorf("no streaming cache hit observed after %d attempts", summary.Attempts)
}

func runScopeScene(ctx context.Context, client *uniai.Client, provider, model, ttl string) (runSummary, error) {
	if !providerSupportsScopedCache(provider) {
		return runSummary{
			Provider: provider,
			Model:    model,
			Scene:    sceneScope,
			TTL:      ttl,
		}, fmt.Errorf("provider %q does not support scoped cache validation in cachetest", provider)
	}
	ctrl, err := cacheControlForTTL(ttl)
	if err != nil {
		return runSummary{}, err
	}

	summary := runSummary{
		Provider:  provider,
		Model:     model,
		Scene:     sceneScope,
		Blocking:  true,
		Streaming: false,
		TTL:       ttl,
	}

	for attempt := 1; attempt <= defaultAttempts; attempt++ {
		warm, _, err := executeChat(ctx, client, scopeOptions(provider, model, ctrl, false, "A"), false)
		if err != nil {
			summary.Attempts = attempt
			return summary, err
		}
		hit, _, err := executeChat(ctx, client, scopeOptions(provider, model, ctrl, false, "B"), false)
		if err != nil {
			summary.Attempts = attempt
			return summary, err
		}
		miss, _, err := executeChat(ctx, client, scopeOptions(provider, model, ctrl, true, "C"), false)
		if err != nil {
			summary.Attempts = attempt
			return summary, err
		}

		summary.Attempts = attempt
		summary.UsageWarm = &warm.Usage
		summary.UsageHit = &hit.Usage
		summary.UsageMiss = &miss.Usage
		if hit.Usage.Cache.CachedInputTokens > 0 &&
			miss.Usage.Cache.CachedInputTokens < hit.Usage.Cache.CachedInputTokens {
			summary.Success = true
			return summary, nil
		}
	}

	return summary, fmt.Errorf("scoped cache behavior was not observed after %d attempts", summary.Attempts)
}

func executeChat(
	ctx context.Context,
	client *uniai.Client,
	baseOpts []uniai.ChatOption,
	stream bool,
) (*uniai.ChatResult, *uniai.Usage, error) {
	opts := append([]uniai.ChatOption{}, baseOpts...)
	var finalUsage *uniai.Usage
	if stream {
		opts = append(opts, uniai.WithOnStream(func(ev uniai.StreamEvent) error {
			if ev.Done && ev.Usage != nil {
				usage := *ev.Usage
				finalUsage = &usage
			}
			return nil
		}))
	}
	resp, err := client.Chat(ctx, opts...)
	if err != nil {
		return nil, nil, err
	}
	if resp == nil {
		return nil, nil, fmt.Errorf("empty response")
	}
	return resp, finalUsage, nil
}

func statsOptions(provider, model string) []uniai.ChatOption {
	opts := []uniai.ChatOption{
		uniai.WithProvider(provider),
		uniai.WithModel(model),
		uniai.WithMaxTokens(defaultMaxTokens),
		uniai.WithMessages(uniai.User(statsPrompt())),
	}
	if cacheOpt := providerCacheOption(provider, "uniai-cachetest-stats", envAny("PROMPT_CACHE_RETENTION", "CACHE_RETENTION")); cacheOpt != nil {
		opts = append(opts, cacheOpt)
	}
	return opts
}

func scopeOptions(provider, model string, ctrl uniai.CacheControl, mutatePrefix bool, suffixLabel string) []uniai.ChatOption {
	prefix := scopedPrefixPrompt()
	if mutatePrefix {
		prefix = strings.Replace(prefix, "durable", "volatile", 1)
	}
	message := uniai.UserParts(
		uniai.WithPartCacheControl(uniai.TextPart(prefix), ctrl),
		uniai.TextPart(scopeSuffixPrompt(suffixLabel)),
	)
	return []uniai.ChatOption{
		uniai.WithProvider(provider),
		uniai.WithModel(model),
		uniai.WithMaxTokens(defaultMaxTokens),
		uniai.WithMessages(message),
	}
}

func providerCacheOption(provider, key, retention string) uniai.ChatOption {
	key = strings.TrimSpace(key)
	retention = strings.TrimSpace(retention)
	if key == "" && retention == "" {
		return nil
	}

	opts := structs.JSONMap{}
	if key != "" {
		opts["prompt_cache_key"] = key
	}
	if retention != "" {
		opts["prompt_cache_retention"] = retention
	}

	switch provider {
	case "openai", "openai_resp":
		return uniai.WithOpenAIOptions(opts)
	case "azure":
		return uniai.WithAzureOptions(opts)
	default:
		return nil
	}
}

func statsPrompt() string {
	return longCachePrefix() + "\n\nReply with exactly: cachetest-ok"
}

func scopedPrefixPrompt() string {
	return longCachePrefix() + "\n\nTreat the preceding content as fixed reference material."
}

func scopeSuffixPrompt(label string) string {
	return fmt.Sprintf("\n\nDynamic suffix %s: reply with exactly cachetest-scope-%s", label, strings.ToLower(strings.TrimSpace(label)))
}

func longCachePrefix() string {
	const sentence = "This is a durable cache prefix sentence about systems, reliability, observability, rollout safety, incident review, cost controls, and API contracts. "
	return strings.Repeat(sentence, 120)
}

func cacheControlForTTL(ttl string) (uniai.CacheControl, error) {
	switch strings.TrimSpace(ttl) {
	case "", "5m":
		return uniai.CacheTTL5m(), nil
	case "1h":
		return uniai.CacheTTL1h(), nil
	default:
		return uniai.CacheControl{}, fmt.Errorf("unsupported ttl %q (supported: 5m, 1h)", ttl)
	}
}

func providerSupportsScopedCache(provider string) bool {
	switch provider {
	case "anthropic", "bedrock":
		return true
	default:
		return false
	}
}

func providerSupportsStreaming(provider string) bool {
	switch provider {
	case "openai", "openai_resp", "azure", "anthropic", "bedrock":
		return true
	default:
		return false
	}
}

func normalizeScene(raw string, forceStream bool) (string, error) {
	scene := normalizeSceneValue(raw)
	if forceStream {
		if scene == sceneScope {
			return "", fmt.Errorf("--stream cannot be combined with scene=%s", sceneScope)
		}
		scene = sceneStream
	}
	switch scene {
	case sceneStats, sceneScope, sceneStream:
		return scene, nil
	default:
		return "", fmt.Errorf("unsupported scene %q (supported: %s, %s, %s)", raw, sceneStats, sceneScope, sceneStream)
	}
}

func normalizeSceneValue(raw string) string {
	scene := strings.ToLower(strings.TrimSpace(raw))
	if scene == "" {
		return defaultScene
	}
	return scene
}

func printSummary(summary runSummary) {
	fmt.Printf("provider=%s\n", summary.Provider)
	fmt.Printf("model=%s\n", summary.Model)
	fmt.Printf("scene=%s\n", summary.Scene)
	fmt.Printf("success=%t\n", summary.Success)
	fmt.Printf("attempts=%d\n", summary.Attempts)
	if summary.UsageHit != nil {
		fmt.Printf("cached_input_tokens=%d\n", summary.UsageHit.Cache.CachedInputTokens)
	}
	if summary.UsageStream != nil {
		fmt.Printf("stream_cached_input_tokens=%d\n", summary.UsageStream.Cache.CachedInputTokens)
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		fmt.Printf("summary_json_error=%v\n", err)
		return
	}
	fmt.Println(string(data))
}

func buildClientConfig(providerRaw, modelRaw string) (uniai.Config, string, error) {
	provider := strings.ToLower(strings.TrimSpace(providerRaw))
	if provider == "" {
		provider = defaultProvider
	}
	modelArg := strings.TrimSpace(modelRaw)

	cfg := uniai.Config{Provider: provider}

	switch provider {
	case "openai", "openai_resp":
		apiKey, err := requireAnyEnv("OPENAI_API_KEY")
		if err != nil {
			return uniai.Config{}, "", fmt.Errorf("%s provider: %w", provider, err)
		}
		model, err := requireModel(modelArg, "OPENAI_MODEL")
		if err != nil {
			return uniai.Config{}, "", fmt.Errorf("%s provider: %w", provider, err)
		}
		cfg.OpenAIAPIKey = apiKey
		cfg.OpenAIAPIBase = envAny("OPENAI_API_BASE")
		cfg.OpenAIModel = model
		return cfg, model, nil
	case "azure":
		apiKey, err := requireAnyEnv("AZURE_OPENAI_API_KEY")
		if err != nil {
			return uniai.Config{}, "", fmt.Errorf("azure provider: %w", err)
		}
		endpoint, err := requireAnyEnv("AZURE_OPENAI_ENDPOINT")
		if err != nil {
			return uniai.Config{}, "", fmt.Errorf("azure provider: %w", err)
		}
		deployment, err := requireModel(modelArg, "AZURE_OPENAI_DEPLOYMENT", "AZURE_OPENAI_MODEL")
		if err != nil {
			return uniai.Config{}, "", fmt.Errorf("azure provider: %w", err)
		}
		cfg.AzureOpenAIAPIKey = apiKey
		cfg.AzureOpenAIEndpoint = endpoint
		cfg.AzureOpenAIModel = deployment
		cfg.AzureOpenAIAPIVersion = envOrDefault("AZURE_OPENAI_API_VERSION", "2024-08-01-preview")
		return cfg, deployment, nil
	case "anthropic":
		apiKey, err := requireAnyEnv("ANTHROPIC_API_KEY", "CLAUDE_API_KEY")
		if err != nil {
			return uniai.Config{}, "", fmt.Errorf("anthropic provider: %w", err)
		}
		model, err := requireModel(modelArg, "ANTHROPIC_MODEL")
		if err != nil {
			return uniai.Config{}, "", fmt.Errorf("anthropic provider: %w", err)
		}
		cfg.AnthropicAPIKey = apiKey
		cfg.AnthropicModel = model
		return cfg, model, nil
	case "bedrock":
		key, err := requireAnyEnv("AWS_ACCESS_KEY_ID")
		if err != nil {
			return uniai.Config{}, "", fmt.Errorf("bedrock provider: %w", err)
		}
		secret, err := requireAnyEnv("AWS_SECRET_ACCESS_KEY")
		if err != nil {
			return uniai.Config{}, "", fmt.Errorf("bedrock provider: %w", err)
		}
		modelArn, err := requireModel(modelArg, "BEDROCK_MODEL_ARN")
		if err != nil {
			return uniai.Config{}, "", fmt.Errorf("bedrock provider: %w", err)
		}
		cfg.AwsKey = key
		cfg.AwsSecret = secret
		cfg.AwsRegion = envOrDefault("AWS_REGION", "us-east-1")
		cfg.AwsBedrockModelArn = modelArn
		return cfg, modelArn, nil
	default:
		return uniai.Config{}, "", fmt.Errorf("unsupported provider %q", provider)
	}
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

func envIntOrDefault(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	val, err := strconv.Atoi(raw)
	if err != nil || val <= 0 {
		return fallback
	}
	return val
}

func envBool(name string) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	switch raw {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
