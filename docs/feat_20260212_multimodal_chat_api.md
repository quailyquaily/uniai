# Multimodal Chat API Proposal (Lean V1, 2026-02-12)

## Status

- Proposal
- Scope: `chat` API only
- Goal: support multimodal chat input with minimal new surface area and strict backward compatibility

## Problem

`uniai` currently uses `Message.Content string` and `Result.Text string` as the main chat payload. Modern models accept structured multimodal input (for example text + image), but current abstraction is text-first and provider-specific multimodal payloads are not first-class.

## First-Principles Decisions

1. Keep one core abstraction: `Message` content as an ordered list of parts.
2. Preserve old API behavior (`Content`, `Text`) as compatibility views.
3. Ship the smallest useful modality set first.
4. Fail explicitly for unsupported modalities; never silently drop data.
5. Defer optional features until real provider demand exists.

## Lean V1 Scope

Included in V1:

- `Message.Parts` and `Result.Parts`
- `Part` type with minimal modalities
- normalization rules (`Parts` preferred, `Content` fallback)
- OpenAI-compatible and Gemini input mapping for image understanding use cases
- strict unsupported-modality errors for providers/models not yet mapped

Deferred to V2:

- `WithOutputModalities(...)`
- stream `PartDelta`
- public `ChatCapabilities` API
- additional part types beyond the minimal set

## Public API Changes (V1)

### Add `Message.Parts` and keep `Message.Content`

```go
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"` // legacy text field
	Parts      []Part     `json:"parts,omitempty"`   // new structured content
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}
```

### Add `Result.Parts` and keep `Result.Text`

```go
type Result struct {
	Text      string     `json:"text,omitempty"`  // legacy aggregated text
	Parts     []Part     `json:"parts,omitempty"` // normalized structured output
	Model     string     `json:"model,omitempty"`
	Messages  []Message  `json:"messages,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Usage     Usage      `json:"usage,omitempty"`
	Raw       any        `json:"raw,omitempty"`
	Warnings  []string   `json:"warnings,omitempty"`
}
```

### Minimal `Part` for V1

```go
type Part struct {
	Type       string `json:"type"` // text|image_url|image_base64
	Text       string `json:"text,omitempty"`
	URL        string `json:"url,omitempty"`
	DataBase64 string `json:"data_base64,omitempty"`
	MIMEType   string `json:"mime_type,omitempty"`
}
```

Rationale:

- `text` covers current behavior.
- `image_url` and `image_base64` cover the primary multimodal input need.
- other part types are postponed to avoid speculative API growth.

### New helper constructors (additive)

- Message helpers:
  - `UserParts(parts ...Part) Message`
  - `SystemParts(parts ...Part) Message`
  - `AssistantParts(parts ...Part) Message`
- Part helpers:
  - `TextPart(text string) Part`
  - `ImageURLPart(url string) Part`
  - `ImageBase64Part(mimeType, dataBase64 string) Part`

Existing helpers remain unchanged:

- `User(text string)`, `System(text string)`, `Assistant(text string)`, `ToolResult(...)`

## Normalization and Validation Rules

### Request normalization

For each message:

1. If `Parts` is non-empty, use `Parts`.
2. Else if `Content` is non-empty, synthesize one `text` part.
3. Else allow empty content only for cases already valid today (for example assistant tool-calls).

### Response normalization

1. Populate `Result.Parts` when structured parts are available from provider output.
2. Always set `Result.Text` as concatenated `text` parts for compatibility.
3. `Result.Text` may be empty if no text part exists.

### Unsupported modality policy

- If a request includes a part type not supported by selected provider/model mapping, return an explicit error with:
  - provider
  - model
  - part type
- Do not silently downgrade, strip, or transform parts.

## Provider Mapping Strategy (V1)

### OpenAI-compatible (`openai`, `openai_custom`, `deepseek`, `xai`, `groq`, `azure`)

Input:

- `text` -> text content part
- `image_url` -> image URL part
- `image_base64` -> image URL part with `data:<mime>;base64,...` format

Output:

- Preserve current text behavior and additionally normalize available text blocks into `Result.Parts`.
- Non-text structured output in chat path is out of V1 scope.

### Gemini (`providers/gemini`)

Input:

- `text` -> `parts.text`
- `image_base64` -> `parts.inlineData`
- `image_url` -> map if API shape supports URL/file reference, otherwise explicit unsupported error

Output:

- Preserve current text behavior and normalize text blocks into `Result.Parts`.
- Image generation/mixed output remains handled by `image` package in V1.

### Anthropic / Cloudflare / Bedrock / Susanoo

- Keep current text-first behavior in V1.
- If `Parts` contains any non-`text` type, return explicit unsupported error.

## Tool-Calling Interactions

- No change in V1.
- `ToolCall`, `ToolChoice`, and `ToolResult(...)` semantics remain identical to current behavior.

## Backward Compatibility

### Source compatibility

- Existing text-only call sites continue to compile and behave the same.
- Risk: external non-keyed struct literals (`Message{...}` positional) may break when fields are added.
- Mitigation: recommend keyed struct literals in docs.

### Behavior compatibility

- Text-only and tool-calling flows are unchanged.
- New functionality is opt-in via `Parts`.

### JSON compatibility

- `parts` is optional and additive.
- Existing payload shapes remain valid.

## Migration Guide

### Legacy usage (unchanged)

```go
resp, err := client.Chat(ctx,
	uniai.WithModel("gpt-5.2"),
	uniai.WithMessages(uniai.User("hello")),
)
fmt.Println(resp.Text)
```

### Multimodal input usage (V1)

```go
resp, err := client.Chat(ctx,
	uniai.WithModel("gpt-5.2"),
	uniai.WithMessages(
		uniai.UserParts(
			uniai.TextPart("Describe this image."),
			uniai.ImageBase64Part("image/png", base64PNG),
		),
	),
)
if err != nil {
	// handle unsupported modality/provider/model explicitly
}
fmt.Println(resp.Text)
```

## Implementation Plan (Lean V1)

1. `chat` package:
   - add `Part`, `Message.Parts`, `Result.Parts`
   - add helper constructors
   - add normalization and validation helpers
2. openai-compatible conversion:
   - map `text|image_url|image_base64` into provider request payload
   - keep text fallback
3. gemini provider:
   - map `text|image_base64` into Gemini parts
   - explicit error for unsupported image URL path when needed
4. other providers:
   - reject non-text parts explicitly in V1
5. docs and tests

## Test Plan (Lean V1)

- Unit tests:
  - request normalization (`Content` -> `text` part fallback)
  - part validation
  - helper constructors
- Provider mapping tests:
  - OpenAI-compatible mapping for `image_url` and `image_base64`
  - Gemini mapping for `image_base64`
  - explicit unsupported errors in providers not yet mapped
- Compatibility tests:
  - existing text-only tests pass without behavior change
  - `Result.Text` compatibility remains intact

## Deferred V2 Topics

1. `WithOutputModalities(...)` and mixed text/image chat output controls
2. streaming `PartDelta`
3. public `ChatCapabilities` discovery API
4. additional part types (`audio_base64`, `file_url`, `refusal`, and others)
5. broader provider multimodal parity

## Summary

Lean V1 keeps the design minimal and defensible: one new structured content concept (`Parts`), one compatibility bridge (`Content`/`Text`), strict validation, and a phased provider rollout. This captures immediate multimodal value without locking `uniai` into unnecessary API surface too early.
