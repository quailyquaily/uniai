package uniai

import (
	"fmt"
	"math"
	"strings"

	"gopkg.in/yaml.v3"
)

const PricingCatalogVersionV1 = "uniai.pricing.v1"

// PricingCatalog is the external price table used by uniai to derive Usage.Cost.
//
// v1 only covers chat cost estimation and uses USD per 1M tokens.
type PricingCatalog struct {
	Version string            `json:"version,omitempty" yaml:"version,omitempty"`
	Chat    []ChatPricingRule `json:"chat,omitempty" yaml:"chat,omitempty"`
}

// ChatPricingRule defines one chat model price entry.
//
// Matching is exact on normalized provider and model strings. Aliases are optional
// explicit alternate model names for the same price. Empty Provider means the rule
// is provider-generic and is considered after provider-specific rules.
type ChatPricingRule struct {
	Provider string   `json:"provider,omitempty" yaml:"provider,omitempty"`
	Model    string   `json:"model" yaml:"model"`
	Aliases  []string `json:"aliases,omitempty" yaml:"aliases,omitempty"`

	InputUSDPerMillion  float64 `json:"input_usd_per_million" yaml:"input_usd_per_million"`
	OutputUSDPerMillion float64 `json:"output_usd_per_million" yaml:"output_usd_per_million"`

	CachedInputUSDPerMillion        *float64 `json:"cached_input_usd_per_million,omitempty" yaml:"cached_input_usd_per_million,omitempty"`
	CacheCreationInputUSDPerMillion *float64 `json:"cache_creation_input_usd_per_million,omitempty" yaml:"cache_creation_input_usd_per_million,omitempty"`

	// CacheCreationInputDetailUSDPerMillion maps provider-specific cache creation
	// counters, such as "ephemeral_5m_input_tokens", to their USD per 1M token rate.
	CacheCreationInputDetailUSDPerMillion map[string]float64 `json:"cache_creation_input_detail_usd_per_million,omitempty" yaml:"cache_creation_input_detail_usd_per_million,omitempty"`
}

// ParsePricingYAML decodes a pricing YAML document into a PricingCatalog and
// validates the supported schema.
func ParsePricingYAML(data []byte) (*PricingCatalog, error) {
	var catalog PricingCatalog
	if err := yaml.Unmarshal(data, &catalog); err != nil {
		return nil, err
	}
	if err := catalog.Validate(); err != nil {
		return nil, err
	}
	return &catalog, nil
}

// Clone returns a deep copy of the pricing catalog.
func (c *PricingCatalog) Clone() *PricingCatalog {
	if c == nil {
		return nil
	}
	out := &PricingCatalog{
		Version: c.Version,
	}
	if len(c.Chat) > 0 {
		out.Chat = make([]ChatPricingRule, len(c.Chat))
		for i := range c.Chat {
			out.Chat[i] = cloneChatPricingRule(c.Chat[i])
		}
	}
	return out
}

// Validate checks that the catalog uses the supported schema and that numeric
// prices are non-negative.
func (c *PricingCatalog) Validate() error {
	if c == nil {
		return nil
	}
	version := strings.TrimSpace(c.Version)
	if version != "" && version != PricingCatalogVersionV1 {
		return fmt.Errorf("unsupported pricing catalog version %q", c.Version)
	}
	for i, rule := range c.Chat {
		if err := validateChatPricingRule(rule); err != nil {
			return fmt.Errorf("chat[%d]: %w", i, err)
		}
	}
	return nil
}

// EstimateChatCost derives a cost estimate from the catalog, provider, model, and
// usage. It returns false when no rule matches or when the matched rule does not
// define all rates required by the usage payload.
func (c *PricingCatalog) EstimateChatCost(provider, model string, usage Usage) (*UsageCost, bool) {
	if c == nil {
		return nil, false
	}
	if usage.Cost != nil {
		return usage.Cost, true
	}
	rule := c.findChatPricingRule(provider, model)
	if rule == nil {
		return nil, false
	}
	return estimateFixedChatCost(*rule, usage)
}

func (c *PricingCatalog) findChatPricingRule(provider, model string) *ChatPricingRule {
	if c == nil {
		return nil
	}
	provider = normalizeProvider(provider)
	model = normalizeModel(model)
	if model == "" {
		return nil
	}

	for i := range c.Chat {
		rule := &c.Chat[i]
		if normalizeProvider(rule.Provider) != provider {
			continue
		}
		if chatPricingRuleMatches(rule, model) {
			return rule
		}
	}
	if provider == "" {
		return nil
	}
	for i := range c.Chat {
		rule := &c.Chat[i]
		if strings.TrimSpace(rule.Provider) != "" {
			continue
		}
		if chatPricingRuleMatches(rule, model) {
			return rule
		}
	}
	return nil
}

func chatPricingRuleMatches(rule *ChatPricingRule, model string) bool {
	if rule == nil {
		return false
	}
	if normalizeModel(rule.Model) == model {
		return true
	}
	for _, alias := range rule.Aliases {
		if normalizeModel(alias) == model {
			return true
		}
	}
	return false
}

func estimateFixedChatCost(rule ChatPricingRule, usage Usage) (*UsageCost, bool) {
	if !hasPricableUsage(usage) {
		return nil, false
	}

	baseInputTokens := usage.InputTokens - usage.Cache.CachedInputTokens - usage.Cache.CacheCreationInputTokens
	if baseInputTokens < 0 {
		baseInputTokens = 0
	}

	inputCost := tokensCost(baseInputTokens, rule.InputUSDPerMillion)
	outputCost := tokensCost(usage.OutputTokens, rule.OutputUSDPerMillion)
	cachedInputCost := 0.0
	cacheCreationCost := 0.0

	if usage.Cache.CachedInputTokens > 0 {
		if rule.CachedInputUSDPerMillion == nil {
			return nil, false
		}
		cachedInputCost = tokensCost(usage.Cache.CachedInputTokens, *rule.CachedInputUSDPerMillion)
	}

	if usage.Cache.CacheCreationInputTokens > 0 {
		remaining := usage.Cache.CacheCreationInputTokens
		for key, tokens := range usage.Cache.Details {
			rate, ok := findDetailRate(rule.CacheCreationInputDetailUSDPerMillion, key)
			if !ok || tokens <= 0 {
				continue
			}
			cacheCreationCost += tokensCost(tokens, rate)
			remaining -= tokens
		}
		if remaining > 0 {
			if rule.CacheCreationInputUSDPerMillion == nil {
				return nil, false
			}
			cacheCreationCost += tokensCost(remaining, *rule.CacheCreationInputUSDPerMillion)
		}
	}

	total := inputCost + cachedInputCost + cacheCreationCost + outputCost
	return &UsageCost{
		Currency:           "USD",
		Estimated:          true,
		Input:              roundUSD(inputCost),
		CachedInput:        roundUSD(cachedInputCost),
		CacheCreationInput: roundUSD(cacheCreationCost),
		Output:             roundUSD(outputCost),
		Total:              roundUSD(total),
	}, true
}

func hasPricableUsage(usage Usage) bool {
	return usage.InputTokens > 0 ||
		usage.OutputTokens > 0 ||
		usage.Cache.CachedInputTokens > 0 ||
		usage.Cache.CacheCreationInputTokens > 0
}

func findDetailRate(rates map[string]float64, key string) (float64, bool) {
	normalized := normalizeDetailKey(key)
	if normalized == "" || len(rates) == 0 {
		return 0, false
	}
	if rate, ok := rates[normalized]; ok {
		return rate, true
	}
	for rawKey, rate := range rates {
		if normalizeDetailKey(rawKey) == normalized {
			return rate, true
		}
	}
	return 0, false
}

func cloneChatPricingRule(in ChatPricingRule) ChatPricingRule {
	out := ChatPricingRule{
		Provider:            in.Provider,
		Model:               in.Model,
		InputUSDPerMillion:  in.InputUSDPerMillion,
		OutputUSDPerMillion: in.OutputUSDPerMillion,
	}
	if len(in.Aliases) > 0 {
		out.Aliases = append([]string{}, in.Aliases...)
	}
	if in.CachedInputUSDPerMillion != nil {
		v := *in.CachedInputUSDPerMillion
		out.CachedInputUSDPerMillion = &v
	}
	if in.CacheCreationInputUSDPerMillion != nil {
		v := *in.CacheCreationInputUSDPerMillion
		out.CacheCreationInputUSDPerMillion = &v
	}
	if len(in.CacheCreationInputDetailUSDPerMillion) > 0 {
		out.CacheCreationInputDetailUSDPerMillion = make(map[string]float64, len(in.CacheCreationInputDetailUSDPerMillion))
		for key, value := range in.CacheCreationInputDetailUSDPerMillion {
			out.CacheCreationInputDetailUSDPerMillion[normalizeDetailKey(key)] = value
		}
	}
	return out
}

func validateChatPricingRule(rule ChatPricingRule) error {
	if strings.TrimSpace(rule.Model) == "" {
		return fmt.Errorf("model is required")
	}
	if rule.InputUSDPerMillion < 0 {
		return fmt.Errorf("input_usd_per_million must be >= 0")
	}
	if rule.OutputUSDPerMillion < 0 {
		return fmt.Errorf("output_usd_per_million must be >= 0")
	}
	if rule.CachedInputUSDPerMillion != nil && *rule.CachedInputUSDPerMillion < 0 {
		return fmt.Errorf("cached_input_usd_per_million must be >= 0")
	}
	if rule.CacheCreationInputUSDPerMillion != nil && *rule.CacheCreationInputUSDPerMillion < 0 {
		return fmt.Errorf("cache_creation_input_usd_per_million must be >= 0")
	}
	for key, value := range rule.CacheCreationInputDetailUSDPerMillion {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("cache_creation_input_detail_usd_per_million key is required")
		}
		if value < 0 {
			return fmt.Errorf("cache_creation_input_detail_usd_per_million[%q] must be >= 0", key)
		}
	}
	return nil
}

func normalizeProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

func normalizeModel(model string) string {
	model = strings.TrimSpace(strings.ToLower(model))
	model = strings.TrimPrefix(model, "models/")
	return model
}

func normalizeDetailKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

func tokensCost(tokens int, usdPerMillion float64) float64 {
	if tokens <= 0 || usdPerMillion <= 0 {
		return 0
	}
	return float64(tokens) * usdPerMillion / 1_000_000
}

func roundUSD(v float64) float64 {
	return math.Round(v*1e12) / 1e12
}
