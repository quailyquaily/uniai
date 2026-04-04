# Native OpenAI Responses Provider Proposal (2026-04-01)

## Status

- Proposal
- Scope: `chat` API only
- Target provider: `openai_resp`
- Verified against official OpenAI docs on 2026-04-01

## Goal

Add a new `openai_resp` provider implemented directly on OpenAI's `/v1/responses` API.

This provider is intentionally not a compatibility layer over Chat Completions. It should follow Responses-native request and response semantics wherever the existing `uniai` `chat` abstraction allows, and expose the remaining provider-specific surface through explicit OpenAI options.

## Problem

The current `providers/openai` implementation is built on Chat Completions. That path is still useful for compatibility and OpenAI-like providers, but it is no longer sufficient for the full current OpenAI feature set:

- GPT-5.4 reasoning + function tools is not fully supported on `/v1/chat/completions`
- `WithReasoningDetails()` is blocked on the current OpenAI path
- Responses-native items such as `function_call`, `function_call_output`, `reasoning`, `previous_response_id`, and hosted tools do not fit cleanly into the Chat Completions provider
- the current `openai` provider is also the shared path for `deepseek`, `xai`, and `groq`, so changing it in-place would create avoidable compatibility risk

## First-Principles Decisions

1. `openai_resp` is a new provider, not a mode flag on `openai`.
2. The existing `openai` provider remains Chat Completions-based and behaviorally stable.
3. `openai_resp` targets OpenAI's Responses API first. Third-party "OpenAI-compatible" servers are not a compatibility goal for this provider.
4. Responses-native behavior wins over Chat Completions parity when the two disagree.
5. Unsupported option mappings must fail explicitly. No silent downgrades to Chat Completions semantics.
6. The public `chat` abstraction remains the primary API, but OpenAI-specific escape hatches are allowed through `WithOpenAIOptions(...)`.

## Why A Separate Provider

Using a distinct provider string is the cleanest cut for this repository:

- `openai` currently means "Chat Completions-like behavior" and is shared with non-OpenAI endpoints in `client.go`
- `openai_resp` will mean "native Responses API behavior"
- callers can opt in per request with `WithProvider("openai_resp")`
- no existing OpenAI-compatible workflow regresses by surprise

This proposal does not change the default provider.

## Product Requirements

1. `WithProvider("openai_resp")` must route to a dedicated provider implementation.
2. `openai_resp` must reuse existing shared OpenAI config fields:
   - `OpenAIAPIKey`
   - `OpenAIAPIBase`
   - `OpenAIModel`
3. `WithReasoningEffort(...)` must map to Responses `reasoning.effort`.
4. `WithReasoningDetails()` must be supported on `openai_resp`.
5. Unified function tools (`chat.Tool`) must work end-to-end on blocking and streaming paths.
6. Structured outputs must work through the existing OpenAI option path.
7. `previous_response_id` must be supported.
8. Raw Responses-native `input` items must be supported as an explicit override.
9. `Result.Raw` must contain the full OpenAI `responses.Response`.
10. `Result.Reasoning` must be populated from Responses reasoning items when requested or returned.
11. Blocking and streaming behavior must both follow Responses-native parsing rules.

## Non-Goals

- No in-place migration of the current `openai` provider
- No automatic fallback from `openai_resp` to `openai`
- No attempt to make `deepseek`, `xai`, or `groq` work through `openai_resp`
- No first-pass expansion of the shared `chat.Tool` abstraction to model every hosted OpenAI tool
- No first-pass public adapter that mirrors `responses.ResponseNewParams` as a new top-level `uniai` API

## Public API Shape

### Provider selection

```go
resp, err := client.Chat(ctx,
	uniai.WithProvider("openai_resp"),
	uniai.WithModel("gpt-5.4"),
	uniai.WithMessages(uniai.User("Solve this carefully.")),
	uniai.WithReasoningEffort(uniai.ReasoningEffortHigh),
	uniai.WithReasoningDetails(),
)
```

### Result ID

Responses-native continuation makes the response ID operationally important. Add a generic `ID` field to `chat.Result`:

```go
type Result struct {
	ID        string           `json:"id,omitempty"`
	Text      string           `json:"text,omitempty"`
	Parts     []Part           `json:"parts,omitempty"`
	Model     string           `json:"model,omitempty"`
	Messages  []Message        `json:"messages,omitempty"`
	ToolCalls []ToolCall       `json:"tool_calls,omitempty"`
	Reasoning *ReasoningResult `json:"reasoning,omitempty"`
	Usage     Usage            `json:"usage,omitempty"`
	Raw       any              `json:"raw,omitempty"`
	Warnings  []string         `json:"warnings,omitempty"`
}
```

Rationale:

- `previous_response_id` is a core Responses API continuation mechanism
- exposing `resp.ID` directly is much better than forcing callers to downcast `resp.Raw`
- the field is generic enough to remain cross-provider safe

## Supported Request Mapping

### Shared `chat` fields

These fields map directly:

- `Request.Model` -> `responses.ResponseNewParams.Model`
- `Options.Temperature` -> `temperature`
- `Options.TopP` -> `top_p`
- `Options.MaxTokens` -> `max_output_tokens`
- `Options.User` -> `user`
- `Options.ReasoningEffort` -> `reasoning.effort`
- `Request.Tools` -> Responses function tools
- `Request.ToolChoice` -> Responses function `tool_choice`

### Shared fields that must fail explicitly

These fields should return request-build errors on `openai_resp`:

- `Options.Stop`
- `Options.PresencePenalty`
- `Options.FrequencyPenalty`
- `Options.ReasoningBudget`

Reason:

- they do not have safe, direct Responses-native equivalents in the current OpenAI Responses API surface
- silently ignoring or emulating them would make provider behavior misleading

## Responses-Native Input Rules

### Default path: build native `input` from `req.Messages`

If the caller does not provide raw `openai.input`, `openai_resp` builds Responses-native `input` items from `req.Messages`.

Mapping rules:

- `system` -> Responses `message` with role `system`
- `user` -> Responses `message` with role `user`
- `assistant` text -> Responses `message` with role `assistant`
- assistant `ToolCalls` -> one Responses `function_call` item per tool call
- `tool` message -> Responses `function_call_output`

User multimodal parts:

- `text` -> `input_text`
- `image_url` -> `input_image`
- `image_base64` -> `input_image` using a `data:` URL

### Important limitation: reasoning items are not representable in `chat.Message`

The current `chat.Message` abstraction cannot represent Responses-native `reasoning` items.

That means:

- exact stateless replay of prior reasoning items cannot be expressed through `req.Messages` alone
- the recommended continuation path for `openai_resp` is `previous_response_id`
- callers that need full manual item replay can use raw `openai.input`

### Assistant ordering rule

`chat.Message` can represent assistant text and assistant tool calls, but not the exact interleaving of Responses-native output items.

For `openai_resp`, the default synthesized order is:

1. assistant text message
2. assistant function call items in declared order

This is deterministic and sufficient for normal function-calling loops, but it is still a compatibility projection, not a perfect re-serialization of prior Responses output.

## OpenAI Option Surface

`WithOpenAIOptions(...)` remains the OpenAI-specific escape hatch, but `openai_resp` must parse it as an explicit allowlist, not as a blind map merge.

### Supported native options

The new provider should accept these OpenAI option keys:

- `background`
- `conversation`
- `include`
- `input`
- `instructions`
- `max_tool_calls`
- `metadata`
- `previous_response_id`
- `prompt`
- `prompt_cache_key`
- `response_format`
- `safety_identifier`
- `service_tier`
- `store`
- `text`
- `tool_choice`
- `tools`
- `top_logprobs`
- `truncation`
- `user`
- `verbosity`

### Conflict rules

These conflicts must return errors:

- `openai.input` with non-empty `req.Messages`
- `openai.reasoning` with `WithReasoningEffort(...)` or `WithReasoningDetails()`
- raw `openai.tool_choice` with `req.ToolChoice`
- `openai.previous_response_id` with `openai.conversation`
- raw `openai.text` with compatibility shortcut keys `response_format` or `verbosity`

### Merge rules

These merges are allowed:

- raw `openai.tools` may include hosted tools, MCP tools, or custom tools
- unified `req.Tools` are converted to function tools and appended to raw `openai.tools`

This keeps the public cross-provider function-tool abstraction working while still exposing Responses-native tool types when the caller opts in.

## Reasoning Behavior

### Request mapping

- `WithReasoningEffort(v)` -> `reasoning.effort = v`
- `WithReasoningDetails()` -> request reasoning summaries via `reasoning.summary`

Default rule:

- `WithReasoningDetails()` sets `reasoning.summary = "auto"` unless the caller provided an explicit raw `openai.reasoning`

### Response mapping

Responses reasoning output maps into `chat.Result.Reasoning`:

- `reasoning.summary[].text` -> `ReasoningResult.Summary`
- `reasoning.content[].text` -> `ReasoningResult.Blocks` with `Type: "thinking"`
- `reasoning.encrypted_content` -> `ReasoningResult.Blocks` with `Type: "encrypted"`

`openai_resp` should not promise unrestricted chain-of-thought text beyond what OpenAI already exposes through the Responses API.

## Structured Outputs

Use the existing OpenAI options path:

- `openai.response_format` maps to `text.format`
- `openai.verbosity` maps to `text.verbosity`

Supported compatibility inputs:

- `"text"`
- `"json_object"`
- `"json_schema"`

If the caller passes raw `openai.text`, the provider should use it directly instead of rebuilding `text.format`.

## Result Mapping

Map the OpenAI `responses.Response` into `chat.Result` as follows:

- `response.ID` -> `Result.ID`
- `response.Model` -> `Result.Model`
- `response.OutputText()` -> `Result.Text`
- text output -> `Result.Parts` text parts
- function call items -> `Result.ToolCalls`
- reasoning items -> `Result.Reasoning`
- `response.Usage.{InputTokens,OutputTokens,TotalTokens}` -> `Result.Usage`
- full typed response -> `Result.Raw`

### `Result.Messages`

`Result.Messages` should be treated as a best-effort compatibility projection only.

Populate it with:

- assistant text messages when output messages exist
- assistant tool-call message when function calls exist

Do not treat `Result.Messages` as a lossless serialization of OpenAI Responses output. Exact native output remains available in `Result.Raw`.

## Streaming Behavior

`openai_resp` must support `Options.OnStream` using `client.Responses.NewStreaming(...)`.

Stream mapping:

- `response.output_text.delta` -> `StreamEvent.Delta`
- `response.function_call_arguments.delta` -> `StreamEvent.ToolCallDelta.ArgsChunk`
- `response.function_call_arguments.done` -> finalize the corresponding tool call name/id/args
- `response.completed` -> emit final `Done: true` and finalize `Usage`

Reasoning summary stream events should be buffered internally and applied to the final `Result.Reasoning`. They do not fit the current `StreamEvent` shape and should not be emitted as fake text deltas.

Failure rules:

- `response.failed` -> return an error
- `response.incomplete` -> return an error that includes the incomplete reason

## Continuation Patterns

### Recommended pattern: `previous_response_id`

For OpenAI-native multi-turn continuation, recommend:

1. read `resp.ID`
2. pass `openai.previous_response_id = resp.ID` on the next call
3. send only the new turn's tool outputs or user input

This is the cleanest path for preserving reasoning state without requiring `uniai` to model every Responses-native item.

### Full manual replay

When the caller needs exact stateless replay of OpenAI items, use raw `openai.input`.

`openai_resp` should not try to infer or reconstruct missing reasoning items from compatibility messages.

## Provider Package Layout

Add a new provider package:

- `providers/openai_resp/openai_resp.go`

Recommended helper split:

- `internal/oairesp/request.go`
- `internal/oairesp/result.go`
- `internal/oairesp/stream.go`
- `internal/oairesp/options.go`

Rationale:

- keep Responses-native logic out of `internal/oaicompat`, which is currently Chat Completions-oriented
- isolate future OpenAI Responses-specific complexity in one place

## Client Wiring

Add a new provider branch in `client.go`:

- `openai_resp`

Config behavior:

- reuse `OpenAIAPIKey`
- reuse `OpenAIAPIBase`
- reuse `OpenAIModel`
- keep `deepseek`, `xai`, and `groq` on the existing `openai` provider path

## SDK Constraint

The repository currently pins:

- `github.com/openai/openai-go/v3 v3.2.0`

Implementation should start by evaluating an SDK bump to the latest stable `openai-go/v3` before building `openai_resp`.

Reason:

- local generated types in `v3.2.0` still document older reasoning enums and older Responses surfaces
- `openai_resp` is meant to follow current OpenAI Responses behavior, not a stale generated snapshot

If the SDK bump causes unacceptable churn, the provider may still start on `v3.2.0`, but that should be treated as a temporary implementation compromise and documented in the PR.

## Tests

Required unit tests:

1. build request maps basic text input to Responses `input`
2. user image parts map to `input_image`
3. assistant tool calls map to `function_call`
4. tool result messages map to `function_call_output`
5. `WithReasoningEffort(...)` maps to `reasoning.effort`
6. `WithReasoningDetails()` maps to `reasoning.summary`
7. unsupported shared options fail explicitly
8. raw `openai.input` conflicts with `req.Messages`
9. unified function tools append to raw OpenAI tools
10. blocking result parsing extracts text, usage, tool calls, reasoning, and ID
11. streaming path emits text deltas and tool-call arg deltas correctly
12. streaming incomplete/failed responses return clear errors

## Implementation Slices

### Slice 1: Plumbing

- add `Result.ID`
- add `providers/openai_resp`
- add client routing

### Slice 2: Blocking request/response

- build native `responses.ResponseNewParams`
- parse blocking `responses.Response`
- support function tools and reasoning

### Slice 3: Streaming

- implement Responses streaming
- accumulate final result state
- emit unified stream events

### Slice 4: Docs

- README provider list and usage examples
- reasoning docs update: OpenAI `WithReasoningDetails()` is supported on `openai_resp`

## Open Questions

1. Should `openai_resp` reject `Options.OnStream` when raw hosted tools are present in V1, or support them from day one if the SDK stream parser already exposes them cleanly?
2. Should `Result.Messages` include projected assistant messages for hosted tool outputs, or should those remain `Raw`-only in V1?
3. Should the repository add a separate OpenAI Responses adapter package later, similar to `chat/openai/adapter.go`, or keep that out of scope for the provider launch?

## References

Verified against official OpenAI docs on 2026-04-01:

- Responses API reference: <https://platform.openai.com/docs/api-reference/responses/create?api-mode=responses>
- Responses vs Chat Completions: <https://platform.openai.com/docs/guides/responses-vs-chat-completions>
- Reasoning guide: <https://platform.openai.com/docs/guides/reasoning>
- Function calling guide: <https://platform.openai.com/docs/guides/function-calling>
- GPT-5.4 model page: <https://developers.openai.com/api/docs/models/gpt-5.4>
