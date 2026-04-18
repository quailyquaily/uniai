package uniai

import (
	"fmt"
	"math"
	"strings"

	"gopkg.in/yaml.v3"
)

const PricingCatalogVersionV1 = "uniai.pricing.v1"

// PricingCatalog is the price table used by uniai to derive Usage.Cost.
//
// v1 only covers chat cost estimation and uses USD per 1M tokens.
type PricingCatalog struct {
	Version string            `json:"version,omitempty" yaml:"version,omitempty"`
	Chat    []ChatPricingRule `json:"chat,omitempty" yaml:"chat,omitempty"`
}

// ChatPricingRule defines one chat model price entry.
//
// Matching is exact on normalized model strings only by default. Aliases are
// optional explicit alternate model names for the same price. InferenceProvider
// is optional metadata, and can be used as an explicit runtime hint via
// EstimateChatCostWithInferenceProvider.
type ChatPricingRule struct {
	InferenceProvider string   `json:"inference_provider,omitempty" yaml:"inference_provider,omitempty"`
	Model             string   `json:"model" yaml:"model"`
	Aliases           []string `json:"aliases,omitempty" yaml:"aliases,omitempty"`

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
	if err := validateUniqueChatPricingRuleModels(c.Chat); err != nil {
		return err
	}
	return nil
}

// EstimateChatCost derives a cost estimate from the catalog, model, and usage.
// It returns false when no rule matches or when the matched rule does not define
// all rates required by the usage payload.
func (c *PricingCatalog) EstimateChatCost(model string, usage Usage) (*UsageCost, bool) {
	return c.EstimateChatCostWithInferenceProvider("", model, usage)
}

// EstimateChatCostWithInferenceProvider derives a cost estimate from the catalog,
// inference provider hint, model, and usage.
//
// If inferenceProvider is empty, or if the catalog has no rule using that
// inference provider, lookup falls back to model-only matching. If the hinted
// provider exists in the catalog, lookup stays within that provider only.
func (c *PricingCatalog) EstimateChatCostWithInferenceProvider(inferenceProvider, model string, usage Usage) (*UsageCost, bool) {
	if c == nil {
		return nil, false
	}
	if usage.Cost != nil {
		return usage.Cost, true
	}
	rule := c.findChatPricingRuleWithInferenceProvider(inferenceProvider, model)
	if rule == nil {
		return nil, false
	}
	return estimateFixedChatCost(*rule, usage)
}

func (c *PricingCatalog) findChatPricingRule(model string) *ChatPricingRule {
	return c.findChatPricingRuleWithInferenceProvider("", model)
}

func (c *PricingCatalog) findChatPricingRuleWithInferenceProvider(inferenceProvider, model string) *ChatPricingRule {
	if c == nil {
		return nil
	}
	candidates := normalizeModelCandidates(model)
	if len(candidates) == 0 {
		return nil
	}
	inferenceProvider = normalizeInferenceProvider(inferenceProvider)

	if inferenceProvider != "" && c.hasInferenceProvider(inferenceProvider) {
		for _, candidate := range candidates {
			for i := range c.Chat {
				rule := &c.Chat[i]
				if normalizeInferenceProvider(rule.InferenceProvider) != inferenceProvider {
					continue
				}
				if chatPricingRuleMatches(rule, candidate) {
					return rule
				}
			}
		}
		return nil
	}

	for _, candidate := range candidates {
		for i := range c.Chat {
			rule := &c.Chat[i]
			if chatPricingRuleMatches(rule, candidate) {
				return rule
			}
		}
	}
	return nil
}

func (c *PricingCatalog) hasInferenceProvider(inferenceProvider string) bool {
	if c == nil || inferenceProvider == "" {
		return false
	}
	for i := range c.Chat {
		if normalizeInferenceProvider(c.Chat[i].InferenceProvider) == inferenceProvider {
			return true
		}
	}
	return false
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
		var ok bool
		cacheCreationCost, ok = estimateCacheCreationCost(rule, usage.Cache)
		if !ok {
			return nil, false
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

func estimateCacheCreationCost(rule ChatPricingRule, cache UsageCache) (float64, bool) {
	remaining := cache.CacheCreationInputTokens
	cost := 0.0

	for key, tokens := range cache.Details {
		rate, ok := findDetailRate(rule.CacheCreationInputDetailUSDPerMillion, key)
		if !ok || tokens <= 0 {
			continue
		}
		if tokens > remaining {
			return 0, false
		}
		cost += tokensCost(tokens, rate)
		remaining -= tokens
	}

	if remaining > 0 {
		if rule.CacheCreationInputUSDPerMillion == nil {
			return 0, false
		}
		cost += tokensCost(remaining, *rule.CacheCreationInputUSDPerMillion)
	}

	return cost, true
}

func cloneChatPricingRule(in ChatPricingRule) ChatPricingRule {
	out := ChatPricingRule{
		InferenceProvider:   in.InferenceProvider,
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
	if err := validateFinitePrice("input_usd_per_million", rule.InputUSDPerMillion); err != nil {
		return err
	}
	if err := validateFinitePrice("output_usd_per_million", rule.OutputUSDPerMillion); err != nil {
		return err
	}
	if rule.InputUSDPerMillion < 0 {
		return fmt.Errorf("input_usd_per_million must be >= 0")
	}
	if rule.OutputUSDPerMillion < 0 {
		return fmt.Errorf("output_usd_per_million must be >= 0")
	}
	if rule.CachedInputUSDPerMillion != nil {
		if err := validateFinitePrice("cached_input_usd_per_million", *rule.CachedInputUSDPerMillion); err != nil {
			return err
		}
		if *rule.CachedInputUSDPerMillion < 0 {
			return fmt.Errorf("cached_input_usd_per_million must be >= 0")
		}
	}
	if rule.CacheCreationInputUSDPerMillion != nil {
		if err := validateFinitePrice("cache_creation_input_usd_per_million", *rule.CacheCreationInputUSDPerMillion); err != nil {
			return err
		}
		if *rule.CacheCreationInputUSDPerMillion < 0 {
			return fmt.Errorf("cache_creation_input_usd_per_million must be >= 0")
		}
	}
	for key, value := range rule.CacheCreationInputDetailUSDPerMillion {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("cache_creation_input_detail_usd_per_million key is required")
		}
		if err := validateFinitePrice(fmt.Sprintf("cache_creation_input_detail_usd_per_million[%q]", key), value); err != nil {
			return err
		}
		if value < 0 {
			return fmt.Errorf("cache_creation_input_detail_usd_per_million[%q] must be >= 0", key)
		}
	}
	return nil
}

func validateUniqueChatPricingRuleModels(rules []ChatPricingRule) error {
	seen := make(map[string]int, len(rules))
	for i, rule := range rules {
		inferenceProvider := normalizeInferenceProvider(rule.InferenceProvider)
		names := append([]string{rule.Model}, rule.Aliases...)
		for _, name := range names {
			normalized := normalizeModel(name)
			if normalized == "" {
				continue
			}
			key := inferenceProvider + "\x00" + normalized
			if prev, ok := seen[key]; ok {
				if inferenceProvider == "" {
					return fmt.Errorf("chat[%d]: model or alias %q conflicts with chat[%d]", i, name, prev)
				}
				return fmt.Errorf("chat[%d]: model or alias %q conflicts with chat[%d] under inference_provider %q", i, name, prev, rule.InferenceProvider)
			}
			seen[key] = i
		}
	}
	return nil
}

func validateFinitePrice(field string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%s must be a finite number", field)
	}
	return nil
}

func normalizeInferenceProvider(inferenceProvider string) string {
	return strings.ToLower(strings.TrimSpace(inferenceProvider))
}

func normalizeModel(model string) string {
	model = strings.TrimSpace(strings.ToLower(model))
	model = strings.TrimPrefix(model, "models/")
	return model
}

func normalizeModelCandidates(model string) []string {
	normalized := normalizeModel(model)
	if normalized == "" {
		return nil
	}

	candidates := []string{normalized}
	if slash := strings.LastIndex(normalized, "/"); slash >= 0 && slash+1 < len(normalized) {
		suffix := normalized[slash+1:]
		if suffix != "" && suffix != normalized {
			candidates = append(candidates, suffix)
		}
	}
	return candidates
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
