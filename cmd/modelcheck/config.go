package main

import (
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/quailyquaily/uniai"
	"gopkg.in/yaml.v3"
)

//go:embed config.example.yaml
var embeddedConfig []byte

type fileConfig struct {
	TimeoutSeconds int             `yaml:"timeout_seconds"`
	MaxTokens      int             `yaml:"max_tokens"`
	CacheAttempts  int             `yaml:"cache_attempts"`
	Providers      []providerGroup `yaml:"providers"`

	// Models is the expanded, validated list built from Providers.
	Models []modelConfig `yaml:"-"`
}

// modelSettings can be set on a provider group (as defaults for its models)
// or on a single model entry (as overrides).
type modelSettings struct {
	ReasoningEffort string   `yaml:"reasoning_effort"`
	MaxTokens       int      `yaml:"max_tokens"`
	JSONMode        string   `yaml:"json_mode"`        // auto | native | prompt
	CacheBreakpoint string   `yaml:"cache_breakpoint"` // auto | on | off
	Cases           []string `yaml:"cases"`
	TimeoutSeconds  int      `yaml:"timeout_seconds"`
}

// providerGroup is one endpoint and the models to test on it. Credentials are
// never stored in the file: api_key_env and api_base_env name env vars.
type providerGroup struct {
	Name          string `yaml:"name"`
	Provider      string `yaml:"provider"`
	APIKeyEnv     string `yaml:"api_key_env"`
	APIBaseEnv    string `yaml:"api_base_env"`
	APIBase       string `yaml:"api_base"`
	modelSettings `yaml:",inline"`
	Models        []modelEntry `yaml:"models"`
}

// modelEntry is either a plain model ID or a mapping with overrides.
type modelEntry struct {
	Model         string `yaml:"model"`
	Name          string `yaml:"name"` // optional label, to test one model twice
	modelSettings `yaml:",inline"`
}

func (e *modelEntry) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		e.Model = n.Value
		return nil
	}
	type plain modelEntry
	return n.Decode((*plain)(e))
}

// modelConfig is one fully resolved model under test.
type modelConfig struct {
	Name     string // group/label
	Group    string
	Provider string // uniai provider route
	Model    string
	modelSettings

	KeyEnvs      []string // first non-empty wins
	BaseEnv      string
	BaseRequired bool // gateways must have a base URL
	APIBase      string
}

// providerEnv lists the default key and base env vars for uniai routes.
var providerEnv = map[string]struct {
	keys []string
	base string
}{
	"openai":      {[]string{"OPENAI_API_KEY"}, "OPENAI_API_BASE"},
	"openai_resp": {[]string{"OPENAI_API_KEY"}, "OPENAI_API_BASE"},
	"deepseek":    {[]string{"DEEPSEEK_API_KEY"}, ""},
	"xai":         {[]string{"XAI_API_KEY"}, ""},
	"groq":        {[]string{"GROQ_API_KEY"}, ""},
	"meta":        {[]string{"META_API_KEY"}, "META_API_BASE"},
	"sakana":      {[]string{"SAKANA_API_KEY"}, "SAKANA_API_BASE"},
	"gemini":      {[]string{"GEMINI_API_KEY"}, "GEMINI_API_BASE"},
	"anthropic":   {[]string{"CLAUDE_API_KEY", "ANTHROPIC_API_KEY"}, "ANTHROPIC_API_BASE"},
}

func loadConfig(path string) (*fileConfig, error) {
	data := embeddedConfig
	if path != "" {
		var err error
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, err
		}
	}
	return parseConfig(data)
}

func parseConfig(data []byte) (*fileConfig, error) {
	var cfg fileConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if len(cfg.Providers) == 0 {
		return nil, fmt.Errorf("config has no providers")
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 180
	}
	if cfg.CacheAttempts <= 0 {
		cfg.CacheAttempts = 3
	}

	groups := map[string]bool{}
	names := map[string]bool{}
	for i, g := range cfg.Providers {
		g.Name = strings.TrimSpace(g.Name)
		if g.Name == "" {
			return nil, fmt.Errorf("providers[%d]: name is required", i)
		}
		if groups[g.Name] {
			return nil, fmt.Errorf("duplicate provider name %q", g.Name)
		}
		groups[g.Name] = true
		if len(g.Models) == 0 {
			return nil, fmt.Errorf("provider %q has no models", g.Name)
		}

		// A known route name selects that route; any other name is treated as
		// an OpenAI-compatible gateway unless provider says otherwise.
		_, knownName := providerEnv[strings.ToLower(g.Name)]
		route := strings.ToLower(strings.TrimSpace(g.Provider))
		if route == "" {
			route = "openai"
			if knownName {
				route = strings.ToLower(g.Name)
			}
		}
		defaults, ok := providerEnv[route]
		if !ok {
			return nil, fmt.Errorf("provider %q: unsupported provider route %q", g.Name, route)
		}

		keyEnvs, baseEnv, baseRequired := defaults.keys, defaults.base, false
		if !knownName {
			// Gateways read <NAME>_API_KEY and <NAME>_API_BASE by default.
			prefix := envPrefix(g.Name)
			keyEnvs, baseEnv, baseRequired = []string{prefix + "_API_KEY"}, prefix+"_API_BASE", true
		}
		if env := strings.TrimSpace(g.APIKeyEnv); env != "" {
			keyEnvs = []string{env}
		}
		if env := strings.TrimSpace(g.APIBaseEnv); env != "" {
			baseEnv, baseRequired = env, true
		}

		for j, e := range g.Models {
			model := strings.TrimSpace(e.Model)
			if model == "" {
				return nil, fmt.Errorf("provider %q models[%d]: model is required", g.Name, j)
			}
			label := strings.TrimSpace(e.Name)
			if label == "" {
				label = model
			}
			m := modelConfig{
				Name:          g.Name + "/" + label,
				Group:         g.Name,
				Provider:      route,
				Model:         model,
				modelSettings: mergeSettings(g.modelSettings, e.modelSettings),
				KeyEnvs:       keyEnvs,
				BaseEnv:       baseEnv,
				BaseRequired:  baseRequired,
				APIBase:       strings.TrimSpace(g.APIBase),
			}
			if names[m.Name] {
				return nil, fmt.Errorf("duplicate model %q; give one a name", m.Name)
			}
			names[m.Name] = true
			if err := validateSettings(m); err != nil {
				return nil, err
			}
			cfg.Models = append(cfg.Models, m)
		}
	}
	return &cfg, nil
}

// mergeSettings applies model overrides on top of group defaults.
func mergeSettings(group, model modelSettings) modelSettings {
	out := group
	if model.ReasoningEffort != "" {
		out.ReasoningEffort = model.ReasoningEffort
	}
	if model.MaxTokens > 0 {
		out.MaxTokens = model.MaxTokens
	}
	if model.JSONMode != "" {
		out.JSONMode = model.JSONMode
	}
	if model.CacheBreakpoint != "" {
		out.CacheBreakpoint = model.CacheBreakpoint
	}
	if model.Cases != nil {
		out.Cases = model.Cases
	}
	if model.TimeoutSeconds > 0 {
		out.TimeoutSeconds = model.TimeoutSeconds
	}
	return out
}

func validateSettings(m modelConfig) error {
	switch m.JSONMode {
	case "", "auto", "native", "prompt":
	default:
		return fmt.Errorf("%s: invalid json_mode %q", m.Name, m.JSONMode)
	}
	switch m.CacheBreakpoint {
	case "", "auto", "on", "off":
	default:
		return fmt.Errorf("%s: invalid cache_breakpoint %q", m.Name, m.CacheBreakpoint)
	}
	for _, c := range m.Cases {
		if !isKnownCase(c) {
			return fmt.Errorf("%s: unknown case %q", m.Name, c)
		}
	}
	return nil
}

// envPrefix turns a provider name like "open-router" into "OPEN_ROUTER".
func envPrefix(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

// resolved holds the credentials found for one model.
type resolved struct {
	keyEnv  string
	apiBase string
}

// resolveEnv finds the API key and base for m. Missing credentials are
// reported as a skip reason rather than an error so a partial set still runs.
func resolveEnv(m modelConfig, getenv func(string) string) (resolved, string) {
	var out resolved
	skip := ""
	switch {
	case m.APIBase != "":
		out.apiBase = m.APIBase
	case m.BaseEnv != "":
		out.apiBase = strings.TrimSpace(getenv(m.BaseEnv))
		if out.apiBase == "" && m.BaseRequired {
			skip = fmt.Sprintf("env %s is empty", m.BaseEnv)
		}
	}
	for _, env := range m.KeyEnvs {
		if strings.TrimSpace(getenv(env)) != "" {
			out.keyEnv = env
			break
		}
	}
	if out.keyEnv == "" {
		skip = fmt.Sprintf("env %s is empty", strings.Join(m.KeyEnvs, " / "))
	}
	return out, skip
}

func buildClientConfig(m modelConfig, r resolved, getenv func(string) string) uniai.Config {
	key := strings.TrimSpace(getenv(r.keyEnv))
	cfg := uniai.Config{Provider: m.Provider}
	switch m.Provider {
	case "gemini":
		cfg.GeminiAPIKey, cfg.GeminiAPIBase, cfg.GeminiModel = key, r.apiBase, m.Model
	case "anthropic":
		cfg.AnthropicAPIKey, cfg.AnthropicAPIBase, cfg.AnthropicModel = key, r.apiBase, m.Model
	default:
		cfg.OpenAIAPIKey, cfg.OpenAIAPIBase, cfg.OpenAIModel = key, r.apiBase, m.Model
	}
	return cfg
}
