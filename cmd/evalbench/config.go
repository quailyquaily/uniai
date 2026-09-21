package main

import (
	"flag"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/quailyquaily/uniai"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/evaluate"
)

type cliOptions struct {
	request                             evaluate.Request
	options                             runOptions
	apiBase, casePath, category, output string
	preset                              string
	limit                               int
	seed                                int64
	list, dryRun                        bool
}

func parseOptions(args []string, getenv func(string) string, stderr io.Writer) (cliOptions, error) {
	o := cliOptions{}
	fs := flag.NewFlagSet("evalbench", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.preset, "preset", getenv("EVALUATE_PRESET"), "comparison preset: gpt-4o-mini|jev|gpt-5.6-luna|jina; empty uses custom configuration")
	provider := fs.String("provider", getenv("EVALUATE_PROVIDER"), "Evaluate provider (default typesafe)")
	model := fs.String("model", getenv("EVALUATE_MODEL"), "model; required for Chat emulation")
	mode := fs.String("mode", getenv("EVALUATE_EMULATION_MODE"), "off|fallback|force (default off for typesafe, force for Chat)")
	effort := fs.String("reasoning-effort", getenv("EVALUATE_REASONING_EFFORT"), "Chat reasoning effort; empty leaves provider default")
	tokens := fs.String("max-tokens", getenv("EVALUATE_MAX_TOKENS"), "positive Chat generation token limit")
	timeout := fs.String("timeout", getenv("EVALUATE_TIMEOUT"), "per-call duration, e.g. 90s (default 90s)")
	fs.StringVar(&o.request.InferenceProvider, "inference-provider", getenv("EVALUATE_INFERENCE_PROVIDER"), "Chat pricing hint")
	fs.StringVar(&o.apiBase, "api-base", getenv("EVALUATE_API_BASE"), "override provider API base")
	fs.StringVar(&o.casePath, "cases", "", "custom JSONL dataset; default embeds 360 cases")
	fs.StringVar(&o.category, "category", "", "run only this category")
	fs.StringVar(&o.output, "output", "", "write JSON report to a new file")
	fs.IntVar(&o.limit, "limit", 0, "max selected cases; 0 means all")
	fs.IntVar(&o.options.Repeat, "repeat", 1, "measured passes over selected cases")
	fs.IntVar(&o.options.Warmup, "warmup", 0, "unmeasured calls before the first pass")
	fs.Int64Var(&o.seed, "seed", 0, "deterministic shuffle before limit; 0 keeps dataset order")
	fs.Float64Var(&o.options.BooleanThreshold, "boolean-threshold", 0.5, "native ProbabilityTrue >= threshold counts as true")
	fs.Float64Var(&o.options.ScoreTolerance, "score-tolerance", 0.5, "maximum score absolute error for a match")
	fs.BoolVar(&o.list, "list", false, "list selected cases without credentials or requests")
	fs.BoolVar(&o.dryRun, "dry-run", false, "validate dataset and print request count without network calls")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if len(fs.Args()) != 0 {
		return o, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	o.preset = strings.TrimSpace(o.preset)
	if o.preset != "" {
		// A preset replaces stale single-model environment values. Explicit CLI
		// flags still win, including empty flags that clear an optional control.
		values := map[string]string{"provider": "openai", "mode": "force", "reasoning-effort": "", "max-tokens": "256", "inference-provider": ""}
		switch o.preset {
		case "gpt-4o-mini":
			values["model"] = "gpt-4o-mini"
		case "jev":
			values["provider"] = "typesafe"
			values["model"] = "jev-1.13.0"
			values["mode"] = "off"
			values["max-tokens"] = ""
		case "gpt-5.6-luna":
			values["model"] = "gpt-5.6-luna"
			values["reasoning-effort"] = "none"
		case "jina":
			values["provider"] = "jina"
			values["model"] = "jina-embeddings-v5-text-small"
			values["mode"] = "off"
			values["max-tokens"] = ""
		default:
			return o, fmt.Errorf("unknown preset %q", o.preset)
		}
		explicit := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
		for name, value := range values {
			if !explicit[name] {
				if err := fs.Set(name, value); err != nil {
					return o, err
				}
			}
		}
	}
	o.request.Provider = strings.ToLower(strings.TrimSpace(*provider))
	if o.request.Provider == "" {
		o.request.Provider = "typesafe"
	}
	o.request.Model = strings.TrimSpace(*model)
	modelEnv := "OPENAI_MODEL"
	switch o.request.Provider {
	case "typesafe":
		modelEnv = "TYPESAFE_MODEL"
	case "jina":
		modelEnv = "JINA_MODEL"
	case "openai", "openai_resp", "deepseek", "xai", "groq", "meta", "sakana":
	case "gemini":
		modelEnv = "GEMINI_MODEL"
	case "anthropic":
		modelEnv = "ANTHROPIC_MODEL"
	case "azure":
		modelEnv = "AZURE_OPENAI_DEPLOYMENT"
	case "bedrock":
		modelEnv = "BEDROCK_MODEL_ARN"
	case "cloudflare":
		modelEnv = "CLOUDFLARE_MODEL"
	default:
		return o, fmt.Errorf("unsupported CLI provider %q", o.request.Provider)
	}
	if o.request.Model == "" {
		o.request.Model = strings.TrimSpace(getenv(modelEnv))
	}
	if o.request.Model == "" && o.request.Provider == "typesafe" {
		o.request.Model = "jev-1.13.0"
	}
	if o.request.Model == "" && o.request.Provider == "jina" {
		o.request.Model = "jina-embeddings-v5-text-small"
	}
	if o.request.Model == "" && !o.list {
		return o, fmt.Errorf("set EVALUATE_MODEL, %s or --model", modelEnv)
	}
	o.request.EmulationMode = evaluate.EmulationMode(strings.TrimSpace(*mode))
	if o.request.EmulationMode == "" {
		o.request.EmulationMode = evaluate.EmulationForce
		if o.request.Provider == "typesafe" || o.request.Provider == "jina" {
			o.request.EmulationMode = evaluate.EmulationOff
		}
	}
	switch o.request.EmulationMode {
	case evaluate.EmulationOff, evaluate.EmulationFallback, evaluate.EmulationForce:
	default:
		return o, fmt.Errorf("unknown mode %q", *mode)
	}
	if o.request.Provider == "typesafe" && o.request.EmulationMode == evaluate.EmulationForce {
		return o, fmt.Errorf("typesafe has no Chat path")
	}
	if o.request.Provider == "jina" && o.request.EmulationMode != evaluate.EmulationOff {
		return o, fmt.Errorf("jina uses Classify; Chat emulation mode must be off")
	}
	if o.request.Provider != "typesafe" && o.request.Provider != "jina" && o.request.EmulationMode == evaluate.EmulationOff {
		return o, fmt.Errorf("%s has no native Evaluate path; use force or fallback", o.request.Provider)
	}
	if *effort != "" || *tokens != "" {
		o.request.EmulationOptions = &evaluate.EmulationOptions{}
		if *effort != "" {
			v := chat.ReasoningEffort(strings.TrimSpace(*effort))
			switch v {
			case chat.ReasoningEffortNone, chat.ReasoningEffortMinimal, chat.ReasoningEffortLow, chat.ReasoningEffortMedium, chat.ReasoningEffortHigh, chat.ReasoningEffortXHigh, chat.ReasoningEffortMax:
			default:
				return o, fmt.Errorf("unknown reasoning effort %q", *effort)
			}
			o.request.EmulationOptions.ReasoningEffort = &v
			if o.request.Provider == "azure" || o.request.Provider == "cloudflare" {
				return o, fmt.Errorf("%s does not map reasoning effort", o.request.Provider)
			}
		}
		if *tokens != "" {
			v, err := strconv.Atoi(*tokens)
			if err != nil || v < 1 {
				return o, fmt.Errorf("max-tokens must be a positive integer")
			}
			o.request.EmulationOptions.MaxTokens = &v
		}
	}
	if (o.request.Provider == "typesafe" || o.request.Provider == "jina") && (o.request.EmulationOptions != nil || o.request.InferenceProvider != "") {
		return o, fmt.Errorf("%s does not accept Chat emulation options or inference-provider", o.request.Provider)
	}
	if *timeout == "" {
		*timeout = "90s"
	}
	var err error
	o.options.Timeout, err = time.ParseDuration(*timeout)
	if err != nil {
		return o, fmt.Errorf("invalid timeout: %w", err)
	}
	if o.limit < 0 {
		return o, fmt.Errorf("limit must be non-negative")
	}
	if err := o.options.validate(); err != nil {
		return o, err
	}
	return o, nil
}

func (o runOptions) validate() error {
	if o.Repeat < 1 || o.Warmup < 0 || o.Timeout <= 0 {
		return fmt.Errorf("repeat and timeout must be positive; warmup must be non-negative")
	}
	if math.IsNaN(o.BooleanThreshold) || o.BooleanThreshold < 0 || o.BooleanThreshold > 1 {
		return fmt.Errorf("boolean-threshold must be finite and in [0,1]")
	}
	if math.IsNaN(o.ScoreTolerance) || math.IsInf(o.ScoreTolerance, 0) || o.ScoreTolerance < 0 {
		return fmt.Errorf("score-tolerance must be finite and non-negative")
	}
	return nil
}

func clientConfig(o cliOptions, getenv func(string) string) (uniai.Config, error) {
	cfg := uniai.Config{}
	provider := o.request.Provider
	keyEnv, baseEnv := "OPENAI_API_KEY", "OPENAI_API_BASE"
	switch provider {
	case "typesafe":
		keyEnv, baseEnv = "TYPESAFE_API_KEY", "TYPESAFE_API_BASE"
	case "jina":
		keyEnv, baseEnv = "JINA_API_KEY", "JINA_API_BASE"
	case "gemini":
		keyEnv, baseEnv = "GEMINI_API_KEY", "GEMINI_API_BASE"
	case "anthropic":
		keyEnv, baseEnv = "ANTHROPIC_API_KEY", "ANTHROPIC_API_BASE"
	case "azure":
		keyEnv, baseEnv = "AZURE_OPENAI_API_KEY", "AZURE_OPENAI_ENDPOINT"
	case "cloudflare":
		keyEnv, baseEnv = "CLOUDFLARE_API_TOKEN", "CLOUDFLARE_API_BASE"
	case "bedrock":
		cfg.AwsKey = getenv("AWS_ACCESS_KEY_ID")
		cfg.AwsSecret = getenv("AWS_SECRET_ACCESS_KEY")
		cfg.AwsSessionToken = getenv("AWS_SESSION_TOKEN")
		cfg.AwsRegion = getenv("AWS_REGION")
		if cfg.AwsKey == "" || cfg.AwsSecret == "" {
			return cfg, fmt.Errorf("AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY are required")
		}
		if o.apiBase != "" {
			return cfg, fmt.Errorf("bedrock does not support an API base override")
		}
		return cfg, nil
	case "deepseek", "xai", "groq", "meta", "sakana":
		keyEnv = strings.ToUpper(provider) + "_API_KEY"
	}
	key := strings.TrimSpace(getenv(keyEnv))
	if key == "" && (provider == "deepseek" || provider == "xai" || provider == "groq" || provider == "meta" || provider == "sakana") {
		key = getenv("OPENAI_API_KEY")
	}
	if key == "" {
		return cfg, fmt.Errorf("environment variable %s is required", keyEnv)
	}
	base := strings.TrimSpace(o.apiBase)
	if base == "" {
		base = strings.TrimSpace(getenv(baseEnv))
	}
	switch provider {
	case "typesafe":
		cfg.TypeSafeAPIKey = key
		cfg.TypeSafeAPIBase = base
	case "jina":
		cfg.JinaAPIKey = key
		cfg.JinaAPIBase = base
	case "gemini":
		cfg.GeminiAPIKey = key
		cfg.GeminiAPIBase = base
	case "anthropic":
		cfg.AnthropicAPIKey = key
		cfg.AnthropicAPIBase = base
	case "azure":
		cfg.AzureOpenAIAPIKey = key
		cfg.AzureOpenAIEndpoint = base
		cfg.AzureOpenAIAPIVersion = getenv("AZURE_OPENAI_API_VERSION")
		if base == "" {
			return cfg, fmt.Errorf("AZURE_OPENAI_ENDPOINT or --api-base is required")
		}
	case "cloudflare":
		cfg.CloudflareAPIToken = key
		cfg.CloudflareAPIBase = base
		cfg.CloudflareAccountID = getenv("CLOUDFLARE_ACCOUNT_ID")
		if cfg.CloudflareAccountID == "" {
			return cfg, fmt.Errorf("CLOUDFLARE_ACCOUNT_ID is required")
		}
	default:
		if base != "" && (provider == "deepseek" || provider == "xai" || provider == "groq") {
			return cfg, fmt.Errorf("%s uses a fixed endpoint; use provider openai for a custom API base", provider)
		}
		cfg.OpenAIAPIKey = key
		cfg.OpenAIAPIBase = base
	}
	return cfg, nil
}
