# Pricing Catalog And Cost Estimation

This document describes how `uniai` derives `Usage.Cost` from its embedded default pricing catalog or from a caller-provided override.

## What This Feature Does

`uniai` ships an embedded default pricing catalog for common chat and image generation models.

By default, `uniai` calculates a local cost estimate from:

- model
- token usage attached to the `Client.Chat()` or `Client.Image()` result
- the embedded default price table

If you provide a `PricingCatalog` through `Config.Pricing`, `uniai` uses your override instead:

- model
- token usage attached to the `Client.Chat()` or `Client.Image()` result
- your own price table

When a rule matches:

- blocking `Chat()` responses populate `resp.Usage.Cost`
- the final streaming event populates `ev.Usage.Cost`
- `Image()` responses populate `resp.Usage.Cost`

When no rule matches, `Usage.Cost` stays `nil`.

When tool emulation triggers, `Usage` and `Usage.Cost` are aggregated across the internal chat requests used to satisfy that single `Client.Chat()` call.

## Scope

Current scope is intentionally narrow:

- supported: chat and image generation cost estimation
- not supported: embeddings, audio, rerank, classify
- currency: USD only
- price unit: USD per 1 million tokens

`Usage.Cost` is a local derived value. It is not an upstream billing record.

## Main API

Relevant types and functions:

- `uniai.PricingCatalog`
- `uniai.ChatPricingRule`
- `uniai.ImagePricingRule`
- `uniai.DefaultPricingCatalog()`
- `uniai.ParsePricingYAML([]byte)`
- `uniai.WithInferenceProvider(...)`
- `(*uniai.PricingCatalog).EstimateChatCostWithInferenceProvider(...)`
- `(*uniai.PricingCatalog).EstimateImageCostWithInferenceProvider(...)`
- `Config.Pricing`

Example:

```go
client := uniai.New(uniai.Config{
	Provider: "openai",
})
```

Override example:

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

Disable example:

```go
client := uniai.New(uniai.Config{
	Provider: "openai",
	Pricing:  &uniai.PricingCatalog{},
})
```

## YAML Format

Example:

```yaml
chat:
  - inference_provider: openai
    model: gpt-5.4
    tiers:
      - max_input_tokens: 270000
        input_usd_per_million: 2.50
        cached_input_usd_per_million: 0.25
        output_usd_per_million: 15.00
      - input_usd_per_million: 5.00
        cached_input_usd_per_million: 0.50
        output_usd_per_million: 22.50

  - inference_provider: anthropic
    model: claude-sonnet-4-6
    input_usd_per_million: 3.00
    cached_input_usd_per_million: 0.30
    cache_creation_input_usd_per_million: 3.75
    cache_creation_input_detail_usd_per_million:
      ephemeral_5m_input_tokens: 3.75
      ephemeral_1h_input_tokens: 6.00
    output_usd_per_million: 15.00

image:
  - inference_provider: openai
    model: gpt-image-2
    aliases:
      - gpt-image-2-2026-04-21
    text_input_usd_per_million: 5.00
    cached_text_input_usd_per_million: 1.25
    image_input_usd_per_million: 8.00
    cached_image_input_usd_per_million: 2.00
    output_usd_per_million: 30.00
```

See [`pricing.example.yaml`](../pricing.example.yaml) for the embedded default catalog source and a fuller example.

## Rule Fields

Each `chat` entry supports these fields:

- `inference_provider`: optional metadata for the underlying model vendor, such as `openai`, `anthropic`, `gemini`
- `model`: required model name
- `aliases`: optional extra model names that should reuse the same price
- `input_usd_per_million`: base input token price for flat rules
- `output_usd_per_million`: output token price for flat rules
- `cached_input_usd_per_million`: optional cached-input token price
- `cache_creation_input_usd_per_million`: optional cache-write token price
- `cache_creation_input_detail_usd_per_million`: optional per-counter override map for provider-specific cache-write counters
- `tiers`: optional request-level price tiers for models whose rates depend on the raw `input_tokens` count of one upstream request

Each rule must use either flat price fields or `tiers`, not both. All prices must be non-negative.

Each `image` entry supports these fields:

- `inference_provider`: optional metadata for the underlying model vendor, such as `openai`
- `model`: required model name
- `aliases`: optional extra model names that should reuse the same price
- `text_input_usd_per_million`: prompt text input token price
- `image_input_usd_per_million`: input image token price, used by edit/reference-image workflows when the provider reports image input tokens
- `output_usd_per_million`: generated image output token price
- `cached_text_input_usd_per_million`: optional cached text input token price
- `cached_image_input_usd_per_million`: optional cached image input token price

All prices must be non-negative.

## Matching Rules

Matching is conservative:

1. model is normalized and matched exactly
2. aliases are checked explicitly
3. if you call `EstimateChatCostWithInferenceProvider(...)` or `EstimateImageCostWithInferenceProvider(...)` and that `inference_provider` exists in the relevant catalog section, matching stays within that provider only
4. if no provider hint is passed, or the hinted provider does not exist in the catalog, matching falls back to model-only lookup
5. if the runtime model looks like `vendor/model`, lookup first tries the full name, then falls back to the suffix `model`
6. model and alias names in YAML must be unique within the same `inference_provider`
7. if multiple rules share the same model across different `inference_provider` values and no usable hint is provided, the first match in YAML order wins

Normalization lowercases names, strips a leading `models/`, and treats `.` between digits as `-`. This lets `grok-4.1-fast-reasoning` match a `grok-4-1-fast-reasoning` rule.

`uniai` does not guess prices from model family names.

That means:

- if the model name is different, no cost is calculated
- if you use deployment names or custom aliases, add them explicitly

If you need to pass an explicit model-vendor hint at runtime, use:

```go
cost, ok := pricing.EstimateChatCostWithInferenceProvider("openai", "gpt-5", usage)
```

For automatic `Client.Chat()` cost annotation, pass the same hint on the request:

```go
resp, err := client.Chat(ctx,
	uniai.WithModel("gpt-5"),
	uniai.WithInferenceProvider("openai"),
	uniai.WithMessages(uniai.User("hello")),
)
```

For automatic `Client.Image()` cost annotation, `WithImageProvider(...)` is used as the image pricing provider hint when it is set. If it is not set, image pricing falls back to model-only matching.

## Go Construction Example

You can build an override catalog directly in Go instead of YAML:

```go
pricing := &uniai.PricingCatalog{
	Chat: []uniai.ChatPricingRule{
		{
			InferenceProvider:        "openai",
			Model:                    "gpt-5.4-mini",
			InputUSDPerMillion:       0.75,
			CachedInputUSDPerMillion: ptr(0.075),
			OutputUSDPerMillion:      4.50,
		},
		{
			InferenceProvider:  "openai",
			Model:              "my-gpt-5-4-deployment",
			InputUSDPerMillion: 2.50,
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

For a matched image rule:

- text input cost = `(input_text_tokens - cached_text_tokens) * text_input_price`
- image input cost = `(input_image_tokens - cached_image_tokens) * image_input_price`
- cached text input cost = `cached_text_tokens * cached_text_input_price`
- cached image input cost = `cached_image_tokens * cached_image_input_price`
- output cost = `output_tokens * output_price`

All prices above are divided by `1_000_000`.

If an image generation response reports only `input_tokens` without a text/image detail split, `uniai` treats those input tokens as prompt text tokens. This fits the current text-to-image generation path. Edit/reference-image calls should use provider detail fields when available so image input and cached input tokens can be priced separately.

## End-To-End Example

```go
pricing, err := uniai.ParsePricingYAML([]byte(`
chat:
  - inference_provider: openai
    model: gpt-5.4-mini
    input_usd_per_million: 0.75
    cached_input_usd_per_million: 0.075
    output_usd_per_million: 4.50
image:
  - inference_provider: openai
    model: gpt-image-2
    text_input_usd_per_million: 5.00
    image_input_usd_per_million: 8.00
    output_usd_per_million: 30.00
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

For image generation:

```go
resp, err := client.Image(ctx,
	uniai.Image("gpt-image-2", "a minimal line-art cat"),
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
  - inference_provider: openai
    model: my-gpt-5-4-deployment
    input_usd_per_million: 2.50
    output_usd_per_million: 15.00
```

### Provider-specific aliases

If your upstream returns a dated or custom alias, add it to `aliases`:

```yaml
chat:
  - inference_provider: gemini
    model: gemini-3.1-pro-preview
    aliases:
      - gemini-3.1-pro-preview-customtools
    input_usd_per_million: 2.00
    cached_input_usd_per_million: 0.20
    output_usd_per_million: 12.00
```

## Failure Modes

`Usage.Cost` stays `nil` when:

- no rule matches the current model
- usage contains cached-input tokens but the matched rule has no `cached_input_usd_per_million`
- usage contains cache-creation tokens but the matched rule has no suitable cache-creation price
- image usage contains cached text or image input tokens but the matched image rule lacks the corresponding cached-input rate
- image usage has no input or output token counts
- you explicitly pass an empty `Config.Pricing`, such as `&uniai.PricingCatalog{}`

YAML parsing fails when:

- required fields are missing
- a numeric price is negative

## Related Files

- User-facing example: [`pricing.example.yaml`](../pricing.example.yaml)
- Design note: [`docs/feat/feat_20260409_external_pricing_catalog_for_cost_estimation.md`](feat/feat_20260409_external_pricing_catalog_for_cost_estimation.md)
