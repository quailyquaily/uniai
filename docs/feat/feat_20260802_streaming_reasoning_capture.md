# Streaming Reasoning Capture (2026-08-02)

## Status

- Implemented
- Scope: deliver provider-exposed readable reasoning through the existing chat stream callback
- Related design: `docs/feat_20260310_reasoning_api.md`

## Goal

When a caller enables reasoning details and streaming, `uniai` should deliver readable reasoning before the final answer whenever the upstream provider streams it.

The API should remain:

```go
resp, err := client.Chat(ctx,
	uniai.WithModel(model),
	uniai.WithMessages(messages...),
	uniai.WithReasoningDetails(),
	uniai.WithOnStream(func(event uniai.StreamEvent) error {
		if event.ReasoningDelta != nil {
			fmt.Print(event.ReasoningDelta.Delta)
		}
		if event.Delta != "" {
			fmt.Print(event.Delta)
		}
		return nil
	}),
)
```

`Chat` still returns the final `Result`. `WithOnStream` remains the only streaming interface.

## Behavior Before This Change

The previous stream event had no reasoning payload:

```go
type StreamEvent struct {
	Delta         string
	ToolCallDelta *ToolCallDelta
	Usage         *Usage
	Raw           any
	Done          bool
}
```

Provider behavior was inconsistent:

- OpenAI-compatible streaming reads `reasoning_content` and stores it in the final assistant message, but does not send it to `OnStream`.
- OpenAI Responses parses reasoning from the completed response, but ignores reasoning summary and reasoning text delta events.
- Anthropic blocking parses thinking blocks, but streaming ignores `thinking_delta` and `signature_delta`.
- Bedrock's current Claude stream parser handles only `text_delta`.
- Native Gemini supports blocking reasoning summaries but rejects all streaming requests.

The missing feature is a reasoning payload on `StreamEvent` plus provider-specific parsing. A new event system is not required.

## Decisions

1. Keep `Chat(..., WithOnStream(...))`.
2. Add `ReasoningDelta` to `StreamEvent`.
3. Callback invocation order is stream order. Do not add another sequence number.
4. Keep the existing `ReasoningResult` and `ReasoningBlock` shapes.
5. Let each provider keep its existing text, tool-call, and usage accumulation.
6. Add only the reasoning state needed by each provider.
7. Keep provider-native IDs and indexes in `StreamEvent.Raw`.
8. Do not add a generic stream accumulator.
9. Do not add a generic final-value reconciliation layer.
10. Do not add replay, usage-accounting, pricing, or general SSE work to this feature.

## Public API

### Reasoning delta

```go
type ReasoningDeltaType string

const (
	ReasoningDeltaSummary  ReasoningDeltaType = "summary"
	ReasoningDeltaThinking ReasoningDeltaType = "thinking"
)

type ReasoningDelta struct {
	Index int
	Type  ReasoningDeltaType
	Delta string
}
```

Field semantics:

- `Delta` is readable text exactly as exposed by the upstream provider.
- `Type=summary` means the provider identifies the text as a reasoning or thought summary.
- `Type=thinking` means the provider exposes it through a thinking or reasoning-content field.
- `Index` distinguishes multiple summaries or thinking blocks of the same type.
- Indexes are zero-based and normalized by the provider adapter.

No signature, encrypted data, or redacted data is included in `ReasoningDelta`. Those values are not readable stream output. Providers continue to preserve them in the final `ReasoningResult`, existing replay fields, or `Raw` where supported.

### Stream event

```go
type StreamEvent struct {
	Delta          string
	ReasoningDelta *ReasoningDelta
	ToolCallDelta  *ToolCallDelta
	Usage          *Usage
	Raw            any
	Done           bool
}
```

The fields keep their current meanings:

- `Delta`: final answer text
- `ReasoningDelta`: readable reasoning text
- `ToolCallDelta`: tool-call metadata or argument chunks
- `Usage`: provider usage
- `Raw`: provider-specific source event
- `Done`: successful stream completion

Providers should normally set one content payload per nonterminal callback. If one upstream chunk contains both reasoning and answer text, emit two callbacks in this order:

1. reasoning
2. answer text

Attach the same `Raw` value to both callbacks when available.

### Callback guarantees

For one request:

- callbacks are synchronous
- callbacks are not concurrent
- callback order is authoritative
- returning an error stops the stream
- context cancellation stops the stream
- successful streams emit exactly one `Done=true` callback
- failed streams do not emit a successful `Done` callback

## Final Result

Keep the current result types:

```go
type ReasoningResult struct {
	Summary []string         `json:"summary,omitempty"`
	Blocks  []ReasoningBlock `json:"blocks,omitempty"`
}

type ReasoningBlock struct {
	Type      string `json:"type,omitempty"`
	Text      string `json:"text,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"`
}
```

Streaming aggregation follows existing provider semantics:

- summary deltas append to `ReasoningResult.Summary[Index]`
- thinking deltas append to the readable `ReasoningBlock` selected by the provider's normalized index
- signature, encrypted data, and redacted data are parsed internally into the relevant final block when the provider returns them

The provider adapter owns this mapping because final response shapes differ across providers. There is no shared accumulator for text, tools, usage, and reasoning.

A small shared helper may be added only if two or more providers use identical indexed string-append behavior. It must not take over provider protocol state.

## `WithReasoningDetails()`

`WithReasoningDetails()` remains the opt-in for normalized readable reasoning:

- request optional reasoning summaries when the provider requires a request flag
- emit `ReasoningDelta`
- populate `Result.Reasoning`

Without it, providers keep current behavior and do not emit normalized reasoning deltas.

### OpenAI-compatible validation

The previous OpenAI Chat Completions implementation rejected `WithReasoningDetails()` before it could reach DeepSeek or Kimi, even though the shared response parser already read their `reasoning_content`.

This feature must distinguish the resolved provider route:

- official OpenAI Chat Completions continues to reject `WithReasoningDetails()`
- `deepseek` accepts it and uses it as an output-normalization switch
- Kimi models accept it and use it as an output-normalization switch
- other compatible routes continue to reject it until their response contract is verified

The compatible adapter must not add an upstream request field merely to expose `reasoning_content`; it parses the field when the upstream returns it.

Existing `Message.ReasoningContent` preservation remains unchanged because it is used for compatible tool-call replay.

## Provider Mapping

### OpenAI-compatible Chat Completions, DeepSeek, and Kimi

Map:

- `delta.reasoning_content` -> `ReasoningDeltaThinking`
- `delta.content` -> `StreamEvent.Delta`
- `delta.tool_calls` -> `StreamEvent.ToolCallDelta`

Implementation requirements:

- use one normalized reasoning index per response choice currently supported by `uniai`
- emit `reasoning_content` immediately when details are enabled
- continue accumulating it into `Message.ReasoningContent`
- populate final `Result.Reasoning` when details are enabled
- preserve the source chunk in `Raw`

DeepSeek and Kimi use the same mapping because both expose streamed reasoning through `delta.reasoning_content`. Blocking and streaming fixtures with the same field must produce equivalent normalized reasoning.

### OpenAI Responses

Map:

- `response.reasoning_summary_text.delta` -> `ReasoningDeltaSummary`
- `response.reasoning_text.delta` -> `ReasoningDeltaThinking`
- `response.output_text.delta` -> `StreamEvent.Delta`
- function-call argument deltas -> `StreamEvent.ToolCallDelta`

The provider keeps a local mapping from upstream item and part identifiers to normalized reasoning indexes. Upstream fields such as `item_id`, `output_index`, `content_index`, `summary_index`, and `sequence_number` remain available through `Raw`.

The completed OpenAI response remains authoritative for the final `Result`. Accumulated reasoning deltas are used for callbacks and as a fallback when a completed field is absent. Any final-value comparison is local to this provider.

Encrypted reasoning content remains in the final `ReasoningResult` as it does in the blocking path. It is not emitted as readable `ReasoningDelta`.

GPT-5.6 Luna uses this `openai_resp` path. With reasoning details enabled, callers receive provider-generated summary deltas. The API does not promise or label those summaries as raw chain-of-thought.

### Anthropic Messages

Map:

- `thinking_delta` -> `ReasoningDeltaThinking`
- `text_delta` -> `StreamEvent.Delta`
- `input_json_delta` -> `StreamEvent.ToolCallDelta`

The provider also parses, without emitting readable deltas:

- `signature_delta` -> final thinking block signature
- `redacted_thinking` -> final redacted reasoning block

Use the Anthropic content-block index to select local reasoning state. Preserve the current block order in the final result.

Do not rewrite the general SSE parser as part of this feature. Extend the current parser only as required by the provider fixtures for thinking events.

Claude Sonnet 5 uses adaptive thinking and `WithReasoningEffort(...)`. It does not use a caller-supplied manual thinking budget. Sampling parameters that Anthropic rejects with adaptive thinking are omitted by the provider adapter.

### Amazon Bedrock

The current Bedrock Claude path uses the Anthropic Messages event JSON inside Bedrock event-stream chunks.

Share the function that maps one decoded Anthropic Messages event into local stream state and normalized callbacks. Native Anthropic keeps its SSE transport parser; Bedrock keeps its event-stream transport parser.

This shared function has protocol behavior and removes duplicated event mapping. It is not a wrapper that only renames another function.

Bedrock request-side reasoning controls must follow the same existing model validation rules as native Anthropic. Migrating to the Converse API is outside this feature.

### Gemini native API

Implement `streamGenerateContent` using the existing `generateContent` request schema.

Map:

- a returned part with `thought=true` and text -> `ReasoningDeltaSummary`
- normal text -> `StreamEvent.Delta`
- function calls -> `StreamEvent.ToolCallDelta`

Preserve existing Gemini tool-call thought signatures exactly as the blocking path does. General replay of signatures attached to non-tool parts is outside this feature and remains visible through `Raw`.

Blocking `generateContent` and streaming `streamGenerateContent` fixtures must produce equivalent normalized text, summaries, and tool calls.

## Live Integration Test

`cmd/stream` tests the public API against live providers. It always enables `WithReasoningDetails()` and `WithOnStream(...)`.

A case passes only when:

1. the callback receives at least one non-empty `ReasoningDelta`
2. the stream emits a final `Done` event

A reasoning value found only in the completed response does not satisfy the streaming requirement.

Run all configured cases:

```sh
go run ./cmd/stream --config cmd/stream/config.example.yaml run
```

Run one case:

```sh
go run ./cmd/stream --config cmd/stream/config.example.yaml run kimi_k3
```

The example config contains:

| Test | Provider route | Model | Expected normalized type |
| --- | --- | --- | --- |
| `deepseek_v4_pro` | `deepseek` | `deepseek-v4-pro` | `thinking` |
| `kimi_k3` | `openai` with Moonshot API base | `kimi-k3` | `thinking` |
| `anthropic_claude_sonnet_5` | `anthropic` | `claude-sonnet-5` | `thinking` |
| `openai_gpt_5_6_luna` | `openai_resp` | `gpt-5.6-luna` | `summary` |

API keys are read from environment variables named by each case's `api_key_ref`. The config contains variable names only and must not contain secrets. Credential mapping for a provider does not imply that every model from that provider exposes readable reasoning.

## Raw Events

Keep the existing `Raw any` field.

- Portable callers use normalized fields.
- Provider-specific callers may inspect `Raw`.
- Reasoning must be available through `ReasoningDelta`; callers must not be forced to parse `Raw` for supported fields.
- Do not add a new raw-event envelope in this feature.
- Do not include authorization headers, API keys, or credential-bearing URLs.

## Out of Scope

The following are separate changes:

- a new public streaming entrypoint
- a generic event kind or event hierarchy
- an extra stream sequence number
- a generic stream accumulator
- a generic final-value reconciliation layer
- a provider-neutral replay-state API
- reasoning-token usage fields
- reasoning-aware pricing changes
- a general SSE parser rewrite
- returning partial results on callback failure
- Bedrock Converse migration
- Gemini Interactions migration

## Implementation Phases

Each phase starts by adding or updating tests for that phase.

### Phase 1: Shared API

Add:

- `ReasoningDeltaType`
- `ReasoningDelta`
- `StreamEvent.ReasoningDelta`
- root-package exports

Tests:

- existing text, tool, usage, and `Done` events remain unchanged
- reasoning callbacks are synchronous and ordered
- reasoning and answer text in one upstream chunk produce reasoning first

### Phase 2: OpenAI-compatible and OpenAI Responses

Add:

- compatible `reasoning_content` callbacks and final normalization
- resolved-provider validation for `WithReasoningDetails()`
- OpenAI Responses summary and reasoning text callbacks

Tests:

- DeepSeek- and Kimi-compatible reasoning-only chunks invoke the callback
- official OpenAI Chat Completions still rejects reasoning details
- compatible blocking and streaming results agree
- OpenAI Responses summary and thinking indexes remain separate
- OpenAI Responses completed values do not duplicate streamed deltas

### Phase 3: Anthropic and Bedrock

Add:

- Anthropic thinking callbacks
- final signature and redacted-block aggregation
- shared Anthropic event mapping for native Anthropic and Bedrock transports

Tests:

- thinking text streams before answer text
- signatures attach to the correct final block
- redacted data remains unchanged
- native Anthropic and Bedrock map equivalent event JSON consistently

### Phase 4: Gemini

Add:

- native `streamGenerateContent`
- streamed thought summaries
- existing tool-call signature preservation

Tests:

- blocking and streaming results agree
- thought summaries stream before answer text when returned in that order
- function-call signatures survive the existing replay helpers

### Phase 5: Verification and documentation

Update public examples and run:

```sh
go test ./...
go test -race ./chat ./cmd/stream ./internal/anthropicstream ./internal/modelcompat ./internal/oaicompat ./providers/openai ./providers/openai_resp ./providers/anthropic ./providers/bedrock ./providers/gemini
go vet ./...
```

## Acceptance Criteria

1. `WithOnStream` emits readable upstream reasoning without a new streaming API.
2. `StreamEvent.Delta` keeps its existing answer-text meaning.
3. Callback order remains the stream order.
4. Blocking and streaming requests produce equivalent normalized reasoning for supported fixtures.
5. DeepSeek- and Kimi-compatible `reasoning_content`, OpenAI Responses summaries/text, Anthropic thinking, Bedrock Claude thinking, and Gemini thought summaries are covered.
6. Opaque signatures and encrypted or redacted data remain out of readable reasoning callbacks.
7. Existing text, tool-call, usage, cancellation, and `Done` behavior does not regress.
8. `cmd/stream` can verify live reasoning deltas without storing API keys in its config.

## Official References

- OpenAI Responses streaming events: <https://developers.openai.com/api/reference/resources/responses/streaming-events>
- OpenAI reasoning items: <https://developers.openai.com/cookbook/examples/responses_api/reasoning_items>
- OpenAI GPT-5.6 Luna: <https://developers.openai.com/api/docs/models/gpt-5.6-luna>
- Anthropic extended thinking: <https://platform.claude.com/docs/en/docs/build-with-claude/extended-thinking>
- Anthropic Claude Sonnet 5: <https://platform.claude.com/docs/en/about-claude/models/whats-new-sonnet-5>
- DeepSeek thinking mode: <https://api-docs.deepseek.com/guides/thinking_mode>
- Moonshot Kimi OpenAI-compatible provider implementation: <https://moonshotai.github.io/kosong/kosong/chat_provider/kimi.html>
- Gemini thinking with `generateContent`: <https://ai.google.dev/gemini-api/docs/generate-content/thinking>
- Gemini `streamGenerateContent`: <https://ai.google.dev/api/generate-content>
- Amazon Bedrock reasoning delta: <https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ReasoningContentBlockDelta.html>
