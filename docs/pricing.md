# Pricing Catalog And Cost Estimation

This document describes how to let `uniai` derive `Usage.Cost` from a caller-provided price table.

## What This Feature Does

`uniai` does not ship built-in model prices.

If you provide a `PricingCatalog` through `Config.Pricing`, `uniai` can calculate a local cost estimate from:

- provider
- model
- token usage returned by the upstream provider
- your own price table

When a rule matches:

- blocking `Chat()` responses populate `resp.Usage.Cost`
- the final streaming event populates `ev.Usage.Cost`

When no rule matches, `Usage.Cost` stays `nil`.

## Scope

Current scope is intentionally narrow:

- supported: chat cost estimation
- not supported: embeddings, image, audio, rerank, classify
- currency: USD only
- price unit: USD per 1 million tokens

`Usage.Cost` is a local derived value. It is not an upstream billing record.

## Main API

Relevant types and functions:

- `uniai.PricingCatalog`
- `uniai.ChatPricingRule`
- `uniai.ParsePricingYAML([]byte)`
- `Config.Pricing`

Example:

```go
pricing, err := uniai.ParsePricingYAML(yamlBytes)
if err != nil {
	return err
}

client := uniai.New(uniai.Config{
	Provider: "openai",
	Pricing:  pricing,
})
```

## YAML Format

The supported schema version is:

```yaml
version: uniai.pricing.v1
```

Example:

```yaml
version: uniai.pricing.v1

chat:
  - provider: openai
    model: gpt-5.4
    input_usd_per_million: 2.50
    cached_input_usd_per_million: 0.25
    output_usd_per_million: 15.00

  - provider: anthropic
    model: claude-sonnet-4-6
    input_usd_per_million: 3.00
    cached_input_usd_per_million: 0.30
    cache_creation_input_usd_per_million: 3.75
    cache_creation_input_detail_usd_per_million:
      ephemeral_5m_input_tokens: 3.75
      ephemeral_1h_input_tokens: 6.00
    output_usd_per_million: 15.00
```

See [`pricing.example.yaml`](../pricing.example.yaml) for a fuller example.

## Rule Fields

Each `chat` entry supports these fields:

- `provider`: optional provider name such as `openai`, `anthropic`, `gemini`
- `model`: required model name
- `aliases`: optional extra model names that should reuse the same price
- `input_usd_per_million`: required base input token price
- `output_usd_per_million`: required output token price
- `cached_input_usd_per_million`: optional cached-input token price
- `cache_creation_input_usd_per_million`: optional cache-write token price
- `cache_creation_input_detail_usd_per_million`: optional per-counter override map for provider-specific cache-write counters

All prices must be non-negative.

## Matching Rules

Matching is conservative:

1. provider is normalized and matched exactly
2. model is normalized and matched exactly
3. aliases are checked explicitly
4. rules with empty `provider` are treated as provider-generic fallback

`uniai` does not guess prices from model family names.

That means:

- if the provider or model name is different, no cost is calculated
- if you use deployment names or custom aliases, add them explicitly

## Go Construction Example

You can build the catalog directly in Go instead of YAML:

```go
pricing := &uniai.PricingCatalog{
	Version: uniai.PricingCatalogVersionV1,
	Chat: []uniai.ChatPricingRule{
		{
			Provider:                 "openai",
			Model:                    "gpt-5.4-mini",
			InputUSDPerMillion:       0.75,
			CachedInputUSDPerMillion: ptr(0.075),
			OutputUSDPerMillion:      4.50,
		},
		{
			Provider:            "azure",
			Model:               "my-gpt-5-4-deployment",
			InputUSDPerMillion:  2.50,
			OutputUSDPerMillion: 15.00,
		},
	},
}
```

```go
func ptr(v float64) *float64 { return &v }
```

## Cost Formula

For a matched chat rule:

- base input cost = `(input_tokens - cached_input_tokens - cache_creation_input_tokens) * input_price`
- cached input cost = `cached_input_tokens * cached_input_price`
- cache creation cost = `cache_creation_input_tokens * cache_creation_price`
- output cost = `output_tokens * output_price`

All prices above are divided by `1_000_000`.

If `cache_creation_input_detail_usd_per_million` is present, matching detail counters are priced first, and only the remaining cache-creation tokens fall back to `cache_creation_input_usd_per_million`.

## End-To-End Example

```go
pricing, err := uniai.ParsePricingYAML([]byte(`
version: uniai.pricing.v1
chat:
  - provider: openai
    model: gpt-5.4-mini
    input_usd_per_million: 0.75
    cached_input_usd_per_million: 0.075
    output_usd_per_million: 4.50
`))
if err != nil {
	return err
}

client := uniai.New(uniai.Config{
	Provider: "openai",
	Pricing:  pricing,
})

resp, err := client.Chat(ctx,
	uniai.WithModel("gpt-5.4-mini"),
	uniai.WithMessages(uniai.User("hello")),
)
if err != nil {
	return err
}

if resp.Usage.Cost != nil {
	fmt.Printf("input=%f output=%f total=%f\n",
		resp.Usage.Cost.Input,
		resp.Usage.Cost.Output,
		resp.Usage.Cost.Total,
	)
}
```

## Common Cases

### Azure deployment names

If your runtime model name is an Azure deployment name, price that deployment name directly:

```yaml
chat:
  - provider: azure
    model: my-gpt-5-4-deployment
    input_usd_per_million: 2.50
    output_usd_per_million: 15.00
```

### Provider-specific aliases

If your upstream returns a dated or custom alias, add it to `aliases`:

```yaml
chat:
  - provider: gemini
    model: gemini-3.1-pro-preview
    aliases:
      - gemini-3.1-pro-preview-customtools
    input_usd_per_million: 2.00
    cached_input_usd_per_million: 0.20
    output_usd_per_million: 12.00
```

## Failure Modes

`Usage.Cost` stays `nil` when:

- `Config.Pricing` is `nil`
- no rule matches the current provider and model
- usage contains cached-input tokens but the matched rule has no `cached_input_usd_per_million`
- usage contains cache-creation tokens but the matched rule has no suitable cache-creation price

YAML parsing fails when:

- schema version is unsupported
- required fields are missing
- a numeric price is negative

## Related Files

- User-facing example: [`pricing.example.yaml`](../pricing.example.yaml)
- Design note: [`docs/feat/feat_20260409_external_pricing_catalog_for_cost_estimation.md`](feat/feat_20260409_external_pricing_catalog_for_cost_estimation.md)
