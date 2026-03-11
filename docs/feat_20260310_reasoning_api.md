# Reasoning API Proposal (2026-03-10)

## Status

- Proposal
- Scope: `chat` API only
- Target providers: `openai`, `gemini`, `anthropic`

## Goal

Add a small, explicit API for controlling reasoning and optionally retrieving reasoning details, without silently leaking provider-specific reasoning settings into requests that did not ask for them.

This proposal is intentionally split into two concerns:

1. reasoning control
2. reasoning details retrieval

These concerns are related but not identical across providers.

## Product Requirements

1. `ReasoningEffort` and `ReasoningBudgetTokens` must both exist.
2. Documentation must clearly say which provider and model family should prefer which interface.
3. If the caller does not opt in, `uniai` must not send reasoning-related request settings.
4. If the caller does not opt in to reasoning details, `uniai` must not attempt to extract or normalize reasoning details.
5. Unsupported provider or model combinations must fail explicitly instead of being silently ignored.

## Proposed Public API

### Request-side controls

```go
type ReasoningEffort string

const (
	ReasoningEffortNone    ReasoningEffort = "none"
	ReasoningEffortMinimal ReasoningEffort = "minimal"
	ReasoningEffortLow     ReasoningEffort = "low"
	ReasoningEffortMedium  ReasoningEffort = "medium"
	ReasoningEffortHigh    ReasoningEffort = "high"
	ReasoningEffortMax     ReasoningEffort = "max"
	ReasoningEffortXHigh   ReasoningEffort = "xhigh"
)

func WithReasoningEffort(v ReasoningEffort) Option
func WithReasoningBudgetTokens(v int) Option
```

`WithReasoningBudgetTokens(...)` accepts provider-specific sentinel values where supported:

- `-1`: provider dynamic mode, if supported
- `0`: disable thinking, if supported
- positive integer: explicit thinking budget

These sentinel values are not portable across providers and must be validated per provider and model.

### Output-side opt-in

```go
func WithReasoningDetails() Option
```

`WithReasoningDetails()` is a pure opt-in flag. If the caller does not pass it, `uniai` does not request provider reasoning summaries or thought blocks, and does not try to parse them into the normalized result.

### Result shape

```go
type ReasoningResult struct {
	Summary []string         `json:"summary,omitempty"`
	Blocks  []ReasoningBlock `json:"blocks,omitempty"`
}

type ReasoningBlock struct {
	Type      string `json:"type,omitempty"`      // summary|thinking|redacted_thinking|encrypted
	Text      string `json:"text,omitempty"`      // human-readable text when available
	Signature string `json:"signature,omitempty"` // provider opaque verification token
	Data      string `json:"data,omitempty"`      // provider opaque encrypted/redacted payload
}

type Result struct {
	// existing fields...
	Reasoning *ReasoningResult `json:"reasoning,omitempty"`
}
```

`ReasoningResult` is intentionally modest:

- `Summary` covers OpenAI and Gemini well.
- `Blocks` covers Anthropic and future opaque provider payloads.
- raw provider responses remain available in `Result.Raw`.

## Strict Opt-In Rules

### No implicit reasoning request settings

If the caller does not use any of:

- `WithReasoningEffort(...)`
- `WithReasoningBudgetTokens(...)`
- `WithReasoningDetails()`

then the provider request must contain no reasoning-specific fields.

Examples:

- no OpenAI `reasoning_effort`
- no Gemini `thinkingConfig`
- no Anthropic `thinking`
- no Anthropic `output_config.effort`
- no OpenAI reasoning summary request
- no Gemini `includeThoughts`

### No implicit reasoning extraction

If the caller does not use `WithReasoningDetails()`, then:

- `Result.Reasoning` must remain `nil`
- streaming must not emit reasoning deltas
- provider parsers may ignore reasoning-only response fields

### Unsupported combinations are errors

When a caller explicitly opts in but the selected provider or model cannot honor the request, return an error during request construction.

Examples:

- `WithReasoningBudgetTokens(...)` on OpenAI
- unsupported `ReasoningEffort` value for a specific Gemini model family
- `WithReasoningDetails()` on an OpenAI code path that still uses Chat Completions instead of Responses
- `WithReasoningDetails()` on an Anthropic model that requires manual thinking, when no thinking budget is available

## Provider Guidance

The following guidance is what the docs and API comments should communicate clearly.

| Provider | Preferred control | Secondary control | `WithReasoningDetails()` | Notes |
| --- | --- | --- | --- | --- |
| OpenAI | `WithReasoningEffort` | `WithReasoningBudgetTokens` unsupported | Supported only on a Responses-based implementation | OpenAI reasoning is effort-based, not budget-based |
| Gemini 3.x | `WithReasoningEffort` | `WithReasoningBudgetTokens` unsupported in the current native path | Supported via thought summaries | Native control is level-based |
| Gemini 2.5 | `WithReasoningBudgetTokens` | `WithReasoningEffort` as compatibility mapping only | Supported via thought summaries | Native control is token-budget-based |
| Anthropic Claude 4.6 | `WithReasoningEffort` | `WithReasoningBudgetTokens` unsupported in the current native path | Supported via thinking blocks | Official docs recommend effort with adaptive thinking |
| Anthropic manual thinking models | `WithReasoningBudgetTokens` | `WithReasoningEffort` where supported | Supported via thinking blocks | Best fit for Opus 4.5 and earlier manual-thinking Claude 4 models |

## Provider Mapping Details

### OpenAI

Preferred API:

- use `WithReasoningEffort(...)`
- reject `WithReasoningBudgetTokens(...)`

Request mapping:

- `WithReasoningEffort("low")` -> OpenAI `reasoning_effort` in Chat Completions, or `reasoning.effort` in Responses
- if `WithReasoningDetails()` is also enabled, request reasoning summary output on the Responses API

Output behavior:

- OpenAI does not expose raw chain-of-thought as a normal text field
- the supported normalized output is reasoning summary text
- raw reasoning text must not be promised as part of the stable `uniai` abstraction

Implementation note:

- the current `providers/openai` implementation uses Chat Completions
- `WithReasoningDetails()` should therefore be documented as blocked until an OpenAI Responses-based path exists

### Gemini

Gemini has two native control styles depending on model family.

#### Gemini 3.x

Preferred API:

- use `WithReasoningEffort(...)`

Request mapping:

- map `ReasoningEffort` to Gemini `thinkingLevel`
- allowed values depend on model family
- reject unsupported values for the selected model instead of silently coercing

Guidance:

- Gemini 3 Pro is a strong fit for `WithReasoningEffort(...)`
- `WithReasoningBudgetTokens(...)` is not supported in the current native Gemini 3 path

#### Gemini 2.5

Preferred API:

- use `WithReasoningBudgetTokens(...)`

Request mapping:

- map `WithReasoningBudgetTokens(v)` to Gemini `thinkingBudget`
- if `WithReasoningEffort(...)` is used, treat it as a compatibility shortcut and map to documented Gemini-equivalent budgets
- reject requests that set both effort and budget together for Gemini because they overlap semantically

Output behavior:

- `WithReasoningDetails()` enables thought-summary retrieval
- for native Gemini API this means enabling thought output and parsing returned thought summaries into `Result.Reasoning.Summary`
- Gemini `thoughtSignature` remains separate tool-call metadata and is not part of `Result.Reasoning`

### Anthropic

Anthropic has two distinct but related controls:

1. thinking mode and thinking budget
2. effort on newer models

Preferred API guidance:

- Claude Opus 4.6 and Sonnet 4.6: prefer `WithReasoningEffort(...)`
- Opus 4.5 and other manual-thinking Claude 4 models: prefer `WithReasoningBudgetTokens(...)`
- docs should describe Anthropic reasoning control as model-family-specific, not one-size-fits-all

Request mapping:

- `WithReasoningBudgetTokens(v)` -> `thinking: {"type":"enabled","budget_tokens": v}`
- `WithReasoningEffort(v)` -> `output_config.effort = ...` only for models that support effort

Output behavior:

- `WithReasoningDetails()` means `uniai` should try to retrieve Claude thinking blocks
- if the selected model supports adaptive thinking, the provider may enable the appropriate thinking mode automatically when output is requested
- if the selected model requires manual thinking and no budget is available, request building should fail explicitly

Normalization rules:

- summarized or full thinking text goes into `ReasoningResult.Blocks`
- human-readable thought summaries may also be copied into `ReasoningResult.Summary`
- redacted opaque blocks stay opaque; preserve them in `Blocks` rather than trying to interpret them

## Recommended `ReasoningBudgetTokens` Values

This section should be mirrored into the eventual user-facing docs. The numbers below are starting points, not guarantees. Real workloads should still be measured for quality, latency, and cost.

### OpenAI

`WithReasoningBudgetTokens(...)` is not supported for OpenAI.

- any budget value should return an error
- callers should use `WithReasoningEffort(...)` instead

### Gemini 2.5

Gemini 2.5 is the main provider where `WithReasoningBudgetTokens(...)` is a native control.

Recommended values:

- `-1`: recommended default when you want Gemini to choose dynamically
- `1024`: low-budget starting point for simple or latency-sensitive reasoning
- `4096`: balanced starting point for general multi-step reasoning
- `8192`: good starting point for harder coding, math, and planning tasks
- `16384` and above: reserve for genuinely difficult tasks after measuring that lower budgets are insufficient

Provider and model caveats:

- Gemini 2.5 Pro supports `-1` and positive budgets, but does not support `0`
- Gemini 2.5 Flash supports `-1`, `0`, and positive budgets
- Gemini 2.5 Flash Lite also supports `-1`, `0`, and positive budgets, but defaults to not thinking unless budget is enabled
- current native Gemini ranges from the official docs are model-specific:
  - Gemini 2.5 Pro: `-1` or `128..32768`
  - Gemini 2.5 Flash: `-1` or `0..24576`
  - Gemini 2.5 Flash Lite: `-1` or `512..24576`
- Gemini 2.5 Pro allows a lower numeric range than Anthropic, but the cross-provider docs should still recommend `1024` as the first practical explicit budget because it is easier to reason about and aligns better with Anthropic usage

Unified API guidance:

- docs should recommend `-1` or `1024 / 4096 / 8192`
- docs should mention `0` only as a Gemini Flash and Flash Lite special case
- if the selected Gemini model does not support the requested sentinel or numeric range, request building must fail explicitly

### Anthropic

Anthropic supports budget-based control on manual-thinking models, but Claude 4.6 models now recommend effort instead.

Recommended values:

- `1024`: recommended first value and minimum supported budget
- `2048` to `4096`: balanced range for moderate reasoning workloads
- `8192` to `16384`: harder coding, analysis, and tool-planning tasks
- above `32768`: use only when measurements justify it; Anthropic warns these requests can become long-running and more likely to hit networking or timeout issues

Provider caveats:

- `budget_tokens` must generally be less than `max_tokens`
- `0` is invalid
- `-1` is invalid for the budget field; adaptive thinking is a separate Anthropic mode and should not be overloaded onto `WithReasoningBudgetTokens(...)`
- on Claude Opus 4.6 and Sonnet 4.6, budget-based control is treated as unsupported in the current implementation; use effort plus adaptive thinking

Unified API guidance:

- docs should recommend starting at `1024` and increasing incrementally
- docs should not recommend `-1` or `0` for Anthropic
- if the selected Anthropic model is effort-first, docs should steer users to `WithReasoningEffort(...)`, while still allowing budget mode only where the provider continues to support it

## Recommended Caller Guidance

### OpenAI

Use:

```go
uniai.WithReasoningEffort(uniai.ReasoningEffortHigh)
```

Do not use:

```go
uniai.WithReasoningBudgetTokens(8192)
```

### Gemini 3.x

Prefer:

```go
uniai.WithReasoningEffort(uniai.ReasoningEffortHigh)
```

### Gemini 2.5

Prefer:

```go
uniai.WithReasoningBudgetTokens(8192)
```

`WithReasoningEffort(...)` may exist as a convenience layer, but docs should describe it as less native than budget control on Gemini 2.5.

### Anthropic

Portable thinking control:

```go
uniai.WithReasoningBudgetTokens(4096)
```

Newer effort-capable models may also use:

```go
uniai.WithReasoningEffort(uniai.ReasoningEffortMedium)
```

Docs should describe Anthropic effort as a model-family-specific feature, not a universal Anthropic capability.

## Interaction Rules

### `WithReasoningEffort(...)` without `WithReasoningDetails()`

Allowed.

This means:

- control how much the model reasons
- do not request reasoning details
- do not populate `Result.Reasoning`

### `WithReasoningDetails()` without reasoning controls

Allowed, but provider-specific behavior applies:

- OpenAI: blocked until Responses-based support exists
- Gemini: enable thought summaries with provider defaults
- Anthropic: enable appropriate thinking mode if the model allows it; otherwise require explicit budget

### `WithReasoningEffort(...)` and `WithReasoningBudgetTokens(...)` together

Provider-specific validation applies:

- OpenAI: error
- Gemini: error
- Anthropic: allowed only where the model supports both semantics meaningfully; otherwise error

## Implementation Notes for This Repository

Current repository state:

- OpenAI currently has a request-side `reasoning_effort` path, but no normalized reasoning output path
- OpenAI currently has a request-side `reasoning_effort` path, but no normalized reasoning details path
- Gemini currently preserves `thought_signature` for tool calling, but does not expose thought summaries
- Anthropic currently parses visible text and tool use, but not thinking blocks
- `chat.Result` currently has no normalized reasoning field

This proposal does not require changing existing non-reasoning behavior.

## Documentation Rules

When this feature lands, public docs must explicitly say:

1. reasoning settings are opt-in
2. no reasoning fields are sent unless the caller opted in
3. no reasoning details are populated unless the caller opted in
4. OpenAI is best matched by `WithReasoningEffort(...)`
5. Gemini 3 is best matched by `WithReasoningEffort(...)`
6. Gemini 2.5 is best matched by `WithReasoningBudgetTokens(...)`
7. Anthropic thinking is best matched by `WithReasoningBudgetTokens(...)`, while Anthropic effort is model-specific
8. `ReasoningBudgetTokens` recommended values are provider-specific, especially `-1` and `0`

## References

Verified against official provider docs on 2026-03-10:

- OpenAI Responses API: <https://platform.openai.com/docs/api-reference/responses/create?api-mode=responses>
- OpenAI reasoning guide: <https://platform.openai.com/docs/guides/reasoning/quickstart>
- OpenAI reasoning best practices: <https://platform.openai.com/docs/guides/reasoning-best-practices>
- Gemini thinking guide: <https://ai.google.dev/gemini-api/docs/thinking>
- Gemini OpenAI compatibility guide: <https://ai.google.dev/gemini-api/docs/openai>
- Anthropic extended thinking: <https://platform.claude.com/docs/en/build-with-claude/extended-thinking>
- Anthropic effort: <https://platform.claude.com/docs/en/build-with-claude/effort>
- Anthropic Claude 4.6 notes: <https://platform.claude.com/docs/en/about-claude/models/whats-new-claude-4-6>
