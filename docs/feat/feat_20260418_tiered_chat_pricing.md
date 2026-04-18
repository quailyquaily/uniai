# Tiered Chat Pricing For Long-Context Models (2026-04-18)

## Status

- Proposal
- Scope: `chat` pricing only
- Target areas:
  - pricing catalog schema
  - request-level price tier selection
  - exact `Usage.Cost` aggregation across internal multi-request flows
- Fixed decisions:
  - drop the YAML `version` field
  - no backward-compatibility mode
  - no separate `v2` catalog version
  - OpenAI long-context boundary uses `>270000` input tokens

## Goal

Support chat models whose token price changes when a single upstream request
crosses a documented input-token threshold.

The result must stay correct for:

1. a single upstream chat request
2. multi-request `Client.Chat()` flows such as tool emulation
3. blocking results and final streaming usage events

## Implementation Checklist

- [x] confirm current multi-request scope in the codebase
- [x] extend `ChatPricingRule` with optional `tiers`
- [x] remove YAML `version` parsing and validation
- [x] add tier validation rules
- [x] resolve rates from a single upstream request's raw `Usage.InputTokens`
- [x] keep flat-rate pricing behavior for non-tiered models
- [x] change tool emulation to accumulate per-request cost before final aggregation
- [x] change final streaming aggregation to merge already-priced prefix cost
- [x] update the embedded default pricing catalog for current tiered models
- [x] add regression tests for tier boundaries and multi-request aggregation
- [x] run `go test ./...`

## Current Findings

Checked against official pricing pages on 2026-04-18.

### Models with input-length pricing tiers

- OpenAI `gpt-5.4`
  - Standard price: input `$2.50`, cached input `$0.25`, output `$15.00` per 1M tokens.
  - The pricing page says the listed standard price applies to context lengths under `270K`.
  - For this repo, the safe implementation rule is: `<=270000` uses the standard tier, `>270000` uses the long-context tier.

- OpenAI `gpt-5.4-pro`
  - Standard price: input `$30.00`, output `$180.00` per 1M tokens.
  - The model page says the long-context session uses `2x input` and `1.5x output`.
  - For this repo, the same `>270000` boundary should apply.

- Google `gemini-3-pro-preview`
  - Paid standard price is split into `<=200k` vs `>200k` prompt tiers for input, output, and context caching.

- Google `gemini-2.5-pro`
  - Paid standard price is split into `<=200k` vs `>200k` prompt tiers for input, output, and context caching.

### Models checked without current long-context tiering

- OpenAI `gpt-5.4-mini` and `gpt-5.4-nano`
  - Current model pages show one text price each, with no extra long-context surcharge noted.

- Anthropic current supported models in this repo
  - Current Claude pricing docs say `Claude Mythos Preview`, `Claude Opus 4.6`, and `Claude Sonnet 4.6` include the full 1M context window at standard pricing.
  - That means the current Claude models in `pricing.example.yaml` do not need long-context tiers today.

- No current input-length tiers were found in the checked official pricing pages for:
  - `Mistral`
  - `Cohere`
  - `Z.AI`
  - `Moonshot`
  - `MiniMax`
  - `DeepSeek`
  - `xAI`
  - `Groq`
  - `Cloudflare Workers AI`

### Important scope note

Other pricing dimensions already exist, but they are not this proposal's target:

- Batch / Flex / Priority processing
- regional endpoint uplifts
- modality-specific input prices
- tool invocation fees
- context-cache storage fees

This proposal is only about request-level input-token threshold tiers for chat
pricing.

## Why The Current Design Is Not Enough

Today a `ChatPricingRule` stores exactly one rate set for a matched model.

That is not enough for:

- OpenAI `gpt-5.4` and `gpt-5.4-pro`
- Gemini `gemini-3-pro-preview`
- Gemini `gemini-2.5-pro`

There is a second problem: `uniai` currently aggregates `Usage` across internal
requests before assigning `Usage.Cost`.

That is correct for plain token totals, but wrong for tiered pricing.

Example:

1. first upstream request uses `200k` input tokens
2. second upstream request uses `100k` input tokens
3. aggregated `Usage.InputTokens` becomes `300k`

If a `>270k` tier is applied after aggregation, the whole `Client.Chat()` call
looks like one long-context request even though neither upstream request crossed
the threshold.

So the pricing unit must be:

- each upstream request first
- the aggregated `Client.Chat()` result second

## Design Principles

- Keep flat pricing working as-is for the common case.
- Model long-context pricing as request-level rate selection, not fake model IDs.
- Do not infer tiers from context window size or model family names.
- Keep selection deterministic and explicit.
- Return no aggregate cost rather than a partial cost when one internal request
  cannot be priced.
- Avoid a general-purpose rule engine. The current need is input-token threshold
  tiers.

## Rejected Approaches

### Fake model names like `gpt-5.4-long`

This is wrong because the upstream model ID does not change. Only the billing
rate changes based on request usage.

### Apply the tier after `Usage` aggregation

This is wrong for tool emulation and any future multi-request orchestration.

### Add a generic expression language for pricing rules

This is unnecessary for the current known cases and would make validation and
maintenance worse.

## Proposed Schema

Directly extend the current `ChatPricingRule`.

Most models stay flat. Only models with documented input-length pricing changes
use `tiers`.

```go
type ChatPricingRule struct {
	InferenceProvider string   `json:"inference_provider,omitempty" yaml:"inference_provider,omitempty"`
	Model             string   `json:"model" yaml:"model"`
	Aliases           []string `json:"aliases,omitempty" yaml:"aliases,omitempty"`

	// Flat rates for models without request-level tiers.
	InputUSDPerMillion                   float64            `json:"input_usd_per_million,omitempty" yaml:"input_usd_per_million,omitempty"`
	OutputUSDPerMillion                  float64            `json:"output_usd_per_million,omitempty" yaml:"output_usd_per_million,omitempty"`
	CachedInputUSDPerMillion             *float64           `json:"cached_input_usd_per_million,omitempty" yaml:"cached_input_usd_per_million,omitempty"`
	CacheCreationInputUSDPerMillion      *float64           `json:"cache_creation_input_usd_per_million,omitempty" yaml:"cache_creation_input_usd_per_million,omitempty"`
	CacheCreationInputDetailUSDPerMillion map[string]float64 `json:"cache_creation_input_detail_usd_per_million,omitempty" yaml:"cache_creation_input_detail_usd_per_million,omitempty"`

	// Optional request-level tiers for models whose rates depend on input tokens.
	Tiers []ChatPricingTier `json:"tiers,omitempty" yaml:"tiers,omitempty"`
}

type ChatPricingTier struct {
	// Inclusive upper bound for this tier.
	// Nil means "and above".
	MaxInputTokens *int `json:"max_input_tokens,omitempty" yaml:"max_input_tokens,omitempty"`

	InputUSDPerMillion                   float64            `json:"input_usd_per_million" yaml:"input_usd_per_million"`
	OutputUSDPerMillion                  float64            `json:"output_usd_per_million" yaml:"output_usd_per_million"`
	CachedInputUSDPerMillion             *float64           `json:"cached_input_usd_per_million,omitempty" yaml:"cached_input_usd_per_million,omitempty"`
	CacheCreationInputUSDPerMillion      *float64           `json:"cache_creation_input_usd_per_million,omitempty" yaml:"cache_creation_input_usd_per_million,omitempty"`
	CacheCreationInputDetailUSDPerMillion map[string]float64 `json:"cache_creation_input_detail_usd_per_million,omitempty" yaml:"cache_creation_input_detail_usd_per_million,omitempty"`
}
```

### Example YAML

```yaml
chat:
  - inference_provider: openai
    model: gpt-4o
    input_usd_per_million: 2.50
    cached_input_usd_per_million: 1.25
    output_usd_per_million: 10.00

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

  - inference_provider: gemini
    model: gemini-2.5-pro
    tiers:
      - max_input_tokens: 200000
        input_usd_per_million: 1.25
        cached_input_usd_per_million: 0.125
        output_usd_per_million: 10.00

      - input_usd_per_million: 2.50
        cached_input_usd_per_million: 0.25
        output_usd_per_million: 15.00
```

### Validation Rules

1. a rule may use either flat rates or `tiers`, but not both
2. `tiers` must not be empty when present
3. `tiers` are evaluated in YAML order
4. `max_input_tokens` values must be strictly increasing
5. at most one open-ended tier is allowed, and it must be last
6. each tier must define a complete rate set for the fields it uses
7. rule matching by `model` / `aliases` / `inference_provider` stays the same

## Tier Selection Semantics

After a rule is matched:

1. if the rule has no `tiers`, use the flat rates
2. if the rule has `tiers`, look at the single upstream request's input-token count
3. choose the first tier whose `max_input_tokens` is nil or `>=` that count
4. if no tier matches, return no cost

The count used here is the raw `Usage.InputTokens` reported for that one
upstream request.

Do not:

- use aggregated `Client.Chat()` input tokens
- subtract `cached_input_tokens`
- subtract `cache_creation_input_tokens`

The selected tier controls all rates for that request:

- input
- cached input
- cache write
- cache-write detail overrides
- output

## Request-Level Aggregation Semantics

This is the most important implementation rule:

- tier selection must happen before request aggregation

That means the correct sequence is:

1. upstream request finishes
2. `uniai` receives that request's `Usage`
3. `uniai` selects the pricing tier for that request only
4. `uniai` computes that request's `UsageCost`
5. if `Client.Chat()` involved multiple upstream requests, sum the resulting costs

Do not:

1. aggregate multiple request usages first
2. run tier selection on the aggregated token count

## Aggregate Cost Behavior

Aggregate cost should stay conservative.

Recommended rule:

- if every internal request is priced successfully, sum the per-request costs
- if any internal request has pricable usage but no matching rate, the final
  aggregate `Usage.Cost` should stay `nil`

This avoids returning a partial total that looks complete.

## Implementation Plan

### 1. Extend the current schema

- extend parsing and validation for tiered rules directly in the current schema
- reject rules that mix flat rates and `tiers`
- keep flat-rule behavior unchanged

### 2. Add rate-resolution helpers

Introduce an internal helper like:

```go
func resolveChatPricingRates(rule ChatPricingRule, usage Usage) (ChatPricingTierRates, bool)
```

This helper should:

- choose flat rates when `tiers` is empty
- choose a matching tier using that request's input-token count when `tiers` is present

### 3. Separate per-request pricing from aggregate pricing

Introduce an internal helper like:

```go
func (c *Client) estimateChatUsageCost(providerName string, req *chat.Request, model string, usage chat.Usage) (*chat.UsageCost, bool)
```

Use it immediately after each upstream response arrives.

### 4. Change tool-emulation aggregation

Current flow aggregates `Usage` first and prices later.

Change it to maintain both:

- aggregated `Usage`
- aggregated `UsageCost`

For tool emulation:

- price the initial upstream response, if any一个 Client.Chat() 内部拆成多次上游请求
- price the tool-decision request
- price the final answer request, if any
- sum those costs only when all priced requests succeed

### 5. Change final streaming aggregation

For `wrapPrefixedChatStreamUsage(...)`:

- keep the existing `Usage` merge
- add parallel merge logic for already-known prefix cost
- price the final streamed request before combining it with the prefix cost

### 6. Add tests

Minimum tests:

- flat pricing still works
- `gpt-5.4` short tier vs long tier selection
- `gemini-2.5-pro` `<=200k` vs `>200k` tier selection
- multi-request aggregation does not trigger a long tier from summed usage alone
- one long request plus one short request sums the two correct costs
- aggregate cost becomes `nil` when one component request cannot be priced

### 7. Update the default catalog

Only after tier support is implemented:

- encode the current official tiered models, including `gpt-5.4`,
  `gpt-5.4-pro`, `gemini-3-pro-preview`, and `gemini-2.5-pro`
- keep comments for other out-of-scope price dimensions

## Open Questions

### OpenAI threshold wording

OpenAI currently uses two different threshold phrasings in official docs:

- pricing page: under `270K`
- model page: `>272K input tokens`

Chosen rule for this repo:

- use `>270000` input tokens as the long-context boundary
- treat `<=270000` as the standard tier
- keep a source note in the catalog comments because OpenAI's docs are not fully aligned

### Cached-input long-context pricing for OpenAI

The OpenAI model page explicitly states `2x input` and `1.5x output` for the
long-context session.

It does not explicitly restate the cached-input long-context number on the model
page text we checked.

So the long-context cached-input value for `gpt-5.4` should be treated as an
inference unless OpenAI publishes it directly in a more explicit table.

## Sources

- OpenAI pricing: https://openai.com/api/pricing/
- OpenAI GPT-5.4 model page: https://developers.openai.com/api/docs/models/gpt-5.4/
- OpenAI GPT-5.4 pro model page: https://developers.openai.com/api/docs/models/gpt-5.4-pro
- Gemini pricing: https://ai.google.dev/gemini-api/docs/pricing
- Anthropic pricing: https://platform.claude.com/docs/en/docs/about-claude/pricing
