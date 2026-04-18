# Pricing Catalog For Cost Estimation (2026-04-09)

## Status

- Proposal
- Scope: `chat` API only
- Target areas:
  - pricing catalog shape
  - YAML contract for pricing input
  - client-side `Usage.Cost` derivation from the active price table

## Goal

Let `uniai` derive `Usage.Cost` from its embedded default catalog or from a caller-provided override, using:

1. model
2. reported `Usage`
3. the active price table
4. an optional runtime `inference_provider` hint

The matching logic must stay explicit and deterministic.

## Why This Shape

Model pricing changes. Deployment names also vary:

- Azure uses deployment names, not public model IDs.
- Bedrock often uses ARNs or provider-specific model IDs.
- OpenAI-compatible endpoints may expose custom model names.

If `uniai` embeds prices, it will either go stale or guess wrong.

So the safer boundary is:

- `uniai` owns the usage-to-cost math
- `uniai` ships a maintained default catalog
- the caller can still override the price table

## Public API

Add an optional pricing catalog to `Config`:

```go
type Config struct {
	// ...
	Pricing *PricingCatalog
}
```

Define the shared Go structure:

```go
const PricingCatalogVersionV1 = "uniai.pricing.v1"

type PricingCatalog struct {
	Version string            `json:"version,omitempty" yaml:"version,omitempty"`
	Chat    []ChatPricingRule `json:"chat,omitempty" yaml:"chat,omitempty"`
}

type ChatPricingRule struct {
	InferenceProvider string   `json:"inference_provider,omitempty" yaml:"inference_provider,omitempty"`
	Model             string   `json:"model" yaml:"model"`
	Aliases           []string `json:"aliases,omitempty" yaml:"aliases,omitempty"`

	InputUSDPerMillion  float64 `json:"input_usd_per_million" yaml:"input_usd_per_million"`
	OutputUSDPerMillion float64 `json:"output_usd_per_million" yaml:"output_usd_per_million"`

	CachedInputUSDPerMillion        *float64           `json:"cached_input_usd_per_million,omitempty" yaml:"cached_input_usd_per_million,omitempty"`
	CacheCreationInputUSDPerMillion *float64           `json:"cache_creation_input_usd_per_million,omitempty" yaml:"cache_creation_input_usd_per_million,omitempty"`
	CacheCreationInputDetailUSDPerMillion map[string]float64 `json:"cache_creation_input_detail_usd_per_million,omitempty" yaml:"cache_creation_input_detail_usd_per_million,omitempty"`
}
```

Add a YAML parser:

```go
func ParsePricingYAML(data []byte) (*PricingCatalog, error)
```

Add catalog helpers:

```go
func (c *PricingCatalog) Validate() error
func (c *PricingCatalog) Clone() *PricingCatalog
func (c *PricingCatalog) EstimateChatCost(model string, usage Usage) (*UsageCost, bool)
func (c *PricingCatalog) EstimateChatCostWithInferenceProvider(inferenceProvider, model string, usage Usage) (*UsageCost, bool)
```

Allow callers to pass a request-scoped hint for automatic `Usage.Cost`
annotation:

```go
func WithInferenceProvider(inferenceProvider string) ChatOption
```

## YAML Contract

Example:

```yaml
version: uniai.pricing.v1
chat:
  - inference_provider: openai
    model: gpt-5.2
    aliases:
      - gpt-5.2-20260401
    input_usd_per_million: 1.25
    output_usd_per_million: 10
    cached_input_usd_per_million: 0.125

  - inference_provider: anthropic
    model: claude-sonnet-4-20250514
    input_usd_per_million: 3
    output_usd_per_million: 15
    cached_input_usd_per_million: 0.30
    cache_creation_input_usd_per_million: 3.75
    cache_creation_input_detail_usd_per_million:
      ephemeral_5m_input_tokens: 3.75
      ephemeral_1h_input_tokens: 6
```

## Matching Rules

First pass keeps matching simple and explicit:

1. normalize `model` to lowercase + trim spaces
2. strip a leading `models/` prefix from model names
3. if the normalized runtime model contains `/`, also keep the suffix after the last `/` as a fallback candidate
4. `EstimateChatCostWithInferenceProvider(...)` prefers rules whose `inference_provider` matches the provided hint, but falls back to model-only lookup when that provider is absent in the catalog
5. a rule matches when the normalized model equals `model` or one of `aliases`
6. model and alias names must be unique within the same `inference_provider`
7. if multiple rules share the same model across different `inference_provider` values and no usable hint is provided, the first matching rule in YAML order wins

No regex matching.
No prefix matching.
No built-in model-family guessing.

If the caller needs a snapshot model, deployment name, or ARN variant to match, the caller should list it explicitly in `model` or `aliases`.

## Cost Semantics

`Usage.Cost` stays a derived local value, not an upstream billing record.

Base formula:

- base input cost: `input_tokens - cached_input_tokens - cache_creation_input_tokens`
- cached input cost: `cached_input_tokens`
- cache creation cost: `cache_creation_input_tokens`
- output cost: `output_tokens`

Important:

- `Usage.InputTokens` remains the provider-reported total input token count.
- cache breakdown fields are additive detail, not replacement totals.
- if usage contains cache-hit or cache-write tokens but the matched rule does not define the needed rate, estimation returns no cost instead of guessing.

## Client Behavior

When `Config.Pricing == nil`:

- `uniai` uses the embedded default pricing catalog

When `Config.Pricing != nil`:

- blocking `Chat()` results try to populate `resp.Usage.Cost`
- final streaming `StreamEvent.Usage` tries to populate `Cost`
- if no rule matches, `Cost` stays nil

## Non-Goals

- no built-in vendor pricing table
- no automatic remote pricing sync
- no first-pass pricing support for embeddings, images, audio, rerank, or classify
- no regex or wildcard matcher in v1

## Example

```go
pricing, err := uniai.ParsePricingYAML(yamlBytes)
if err != nil {
	return err
}

client := uniai.New(uniai.Config{
	Provider: "openai",
	Pricing:  pricing,
})

resp, err := client.Chat(ctx,
	uniai.WithModel("gpt-5.2"),
	uniai.WithInferenceProvider("openai"),
	uniai.WithMessages(uniai.User("hello")),
)
if err != nil {
	return err
}

if resp.Usage.Cost != nil {
	fmt.Printf("estimated cost: %s %.8f\n", resp.Usage.Cost.Currency, resp.Usage.Cost.Total)
}
```
