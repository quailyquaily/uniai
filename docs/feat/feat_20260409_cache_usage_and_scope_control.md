# Cache Usage Accounting And Scoped Prompt Cache Proposal (2026-04-09)

## Status

- Proposal
- Scope: `chat` API only
- Target areas:
  - unified cache usage reporting
  - scoped cache control where the upstream API supports explicit cache checkpoints
  - runnable `cmd/` verification program using real provider credentials from environment variables
- Verified against official provider docs on 2026-04-09:
  - OpenAI Prompt Caching
  - Azure OpenAI Prompt Caching
  - Anthropic Prompt Caching
  - Amazon Bedrock Prompt Caching
  - Gemini Cached Content

## Implementation Checklist

- [x] Extend shared `chat.Usage` with cache usage fields
- [x] Add shared `CacheControl` and helpers for `Part` and `Tool`
- [x] Surface cache-hit token counts on `openai`, `openai_resp`, and `azure`
- [x] Surface cache hit/write usage on `anthropic`
- [x] Surface cache usage on `bedrock`
- [x] Support scoped cache control on `anthropic`
- [x] Support scoped cache control on `bedrock` user/assistant message text parts
- [x] Reject explicit scoped cache control on unsupported providers
- [x] Add unit tests for request mapping and usage parsing
- [x] Add `cmd/cachetest` with `stats`, `scope`, and `stream` scenes
- [ ] Update top-level README examples and provider docs
- [ ] Run manual live-provider validation with real credentials

## Goal

Enable two capabilities for `uniai` chat callers:

1. Read cache-related token statistics in a unified way from `Result.Usage` and final streaming usage events.
2. Explicitly mark the cacheable boundary of the prompt when the selected upstream provider supports scoped cache control.

This feature is intended to make prompt caching observable and controllable enough for production callers to reason about latency and cost.

## Problem

Current `uniai` behavior is incomplete for prompt caching:

- `chat.Usage` only exposes `input_tokens`, `output_tokens`, and `total_tokens`.
- OpenAI, OpenAI Responses, and Azure can return cache-hit token counts, but `uniai` drops those fields.
- Anthropic and Bedrock can return both cache-read and cache-write token counts, but `uniai` currently does not model them.
- Anthropic and Bedrock support explicit cache checkpoints in request content, but `uniai` has no public API for callers to mark cache boundaries.
- Gemini has a cache feature (`cachedContents`) but it is a separate resource-oriented flow, not an inline cache-boundary mechanism, so it does not fit the current `chat.Request` shape without additional design work.

As a result, callers cannot answer basic operational questions such as:

- Was this response served from cache?
- How many input tokens were saved by cache hits?
- How many tokens were written into cache?
- Did my explicit cache boundary behave as intended?

## User Outcomes

After this feature lands, a caller should be able to:

- inspect `resp.Usage.Cache` for cache-hit and cache-write statistics
- inspect final stream usage and get the same cache counters as the blocking path
- mark reusable prompt sections in a provider-neutral way when using Anthropic or supported Bedrock paths
- fail fast when using explicit cache controls with a provider that cannot express them
- run a real-provider validation CLI under `cmd/` to confirm behavior against live APIs

## Design Principles

1. Keep the existing `Usage.InputTokens`, `Usage.OutputTokens`, and `Usage.TotalTokens` semantics unchanged.
2. Model cache stats as an additive breakdown, not a replacement for existing usage fields.
3. Expose scoped cache control only through a provider-neutral abstraction that describes intent, not raw upstream field names.
4. Fail explicitly when the caller requests scoped cache control on an unsupported provider.
5. Preserve raw provider responses in `Result.Raw` for debugging and provider-specific inspection.
6. Keep first-pass support focused on providers that already map cleanly into the existing `chat` abstraction.

## Product Requirements

1. `chat.Usage` must expose unified cache statistics.
2. Blocking `Result.Usage` and final `StreamEvent.Usage` must carry the same cache fields.
3. `openai`, `openai_resp`, and `azure` must surface cache-hit token counts when the upstream response includes them.
4. `anthropic` must surface cache-hit and cache-write token counts, plus provider cache-creation breakdowns when present.
5. `bedrock` must surface cache-hit and cache-write token counts when the upstream response includes them.
6. Callers must be able to mark explicit cache boundaries on content parts and tool definitions through a shared API.
7. Providers that support scoped cache control must map the shared API to upstream request fields.
8. Providers that do not support scoped cache control must return request-build errors when explicit cache controls are present.
9. `cmd/` must include a runnable live-provider verification program that reads configuration from environment variables and validates expected cache behavior.
10. Unit tests must cover request mapping and response parsing for all supported cache fields.

## Non-Goals

- No first-pass support for Gemini `cachedContents`
- No first-pass support for cache invalidation or explicit cache deletion
- No first-pass attempt to infer or emulate scoped cache control on OpenAI-like providers
- No promise that live cache behavior is deterministic on every request; validation must account for best-effort upstream behavior
- No first-pass provider-agnostic top-level helper for OpenAI `prompt_cache_key`; callers can continue to use provider options for that field

## Public API Proposal

### Unified usage model

Add a nested cache usage breakdown to `chat.Usage`:

```go
type Usage struct {
	InputTokens  int        `json:"input_tokens"`
	OutputTokens int        `json:"output_tokens"`
	TotalTokens  int        `json:"total_tokens"`
	Cache        UsageCache `json:"cache,omitempty"`
}

type UsageCache struct {
	// Tokens read from cache and therefore counted as cache hits.
	CachedInputTokens int `json:"cached_input_tokens,omitempty"`

	// Tokens written to cache for future reuse.
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`

	// Provider-specific cache creation breakdowns.
	// Examples:
	// - Anthropic: ephemeral_5m_input_tokens, ephemeral_1h_input_tokens
	// - Bedrock: provider-dependent TTL buckets
	Details map[string]int `json:"details,omitempty"`
}
```

Mapping rules:

- OpenAI / OpenAI Responses / Azure:
  - `cached_tokens` -> `Usage.Cache.CachedInputTokens`
- Anthropic:
  - `cache_read_input_tokens` -> `Usage.Cache.CachedInputTokens`
  - `cache_creation_input_tokens` -> `Usage.Cache.CacheCreationInputTokens`
  - `cache_creation.*` -> `Usage.Cache.Details`
- Bedrock:
  - cache-read tokens -> `Usage.Cache.CachedInputTokens`
  - cache-write tokens -> `Usage.Cache.CacheCreationInputTokens`
  - cache detail breakdowns -> `Usage.Cache.Details`

Important semantic rule:

- `Usage.InputTokens` remains the provider's reported total input token count.
- `Usage.Cache.CachedInputTokens` is additional breakdown data and must not be subtracted from `InputTokens`.

### Scoped cache control model

Add a provider-neutral cache checkpoint hint:

```go
type CacheControl struct {
	// Empty means provider default TTL.
	// First-pass supported values:
	// - ""
	// - "5m"
	// - "1h"
	TTL string `json:"ttl,omitempty"`
}
```

Attach it to `Part` and `Tool`:

```go
type Part struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	URL          string        `json:"url,omitempty"`
	DataBase64   string        `json:"data_base64,omitempty"`
	MIMEType     string        `json:"mime_type,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type Tool struct {
	Type         string        `json:"type"`
	Function     ToolFunction  `json:"function"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}
```

Helper constructors should be added for ergonomics:

```go
func WithPartCacheControl(part Part, ctrl CacheControl) Part
func WithToolCacheControl(tool Tool, ctrl CacheControl) Tool
func CacheTTL5m() CacheControl
func CacheTTL1h() CacheControl
```

Rationale:

- Anthropic and Bedrock explicit caching is defined by placing a cache marker on a content block or tool boundary.
- The shared abstraction should express "cache up to here" without leaking upstream field names like `cache_control` vs `cachePoint`.
- Part-level control is granular enough for `system`, `user`, and `assistant` text blocks while fitting the current `Message.Parts` design.

### Explicit behavior on unsupported providers

If any request part or tool definition contains `CacheControl` and the selected provider cannot express scoped cache control, the provider must fail with a request-build error.

Examples:

- `openai` with `Part.CacheControl != nil` -> error
- `openai_resp` with `Part.CacheControl != nil` -> error
- `azure` with `Part.CacheControl != nil` -> error
- `cloudflare` with `Part.CacheControl != nil` -> error
- `gemini` with `Part.CacheControl != nil` -> error

This is better than silent ignore because cache scope is an explicit caller intent.

## Provider Support Matrix

### Phase 1 target

| Provider | Cache stats | Scoped cache control | Notes |
|---|---|---|---|
| `openai` | Yes, cache-hit only | No | Support `prompt_cache_key` and `prompt_cache_retention` through OpenAI options |
| `openai_resp` | Yes, cache-hit only | No | Support `prompt_cache_key` and `prompt_cache_retention` through OpenAI options |
| `azure` | Yes, cache-hit only | No | Uses OpenAI-compatible Chat Completions path in current repo |
| `anthropic` | Yes, hit + write + detail breakdown | Yes | Shared `CacheControl` maps to Anthropic `cache_control` on supported parts/tools |
| `bedrock` | Yes, hit + write + detail breakdown when present | Yes, limited | First pass targets the current Claude-style `InvokeModel` request path and currently supports message text-part cache markers only |
| `cloudflare` | No | No | No current cache feature mapping in repo scope |
| `gemini` | No | No | Gemini uses separate `cachedContents` resources; follow-up design needed |

### OpenAI-family notes

Official OpenAI and Azure prompt caching is automatic and prefix-based:

- callers can influence routing with `prompt_cache_key`
- callers can set retention policy with `prompt_cache_retention` where supported
- callers cannot place explicit cache boundaries inside the message list

For these providers, this proposal only standardizes usage accounting, not scoped cache control.

### Anthropic notes

Anthropic prompt caching supports explicit cache markers and usage breakdowns:

- explicit cache marker on tools, system content, and message content
- usage includes cache-read tokens
- usage includes cache-creation tokens
- usage may include TTL-specific creation breakdowns

The shared `CacheControl.TTL` should map to Anthropic `cache_control.ttl`.

### Bedrock notes

Bedrock prompt caching supports explicit checkpoints and read/write usage metrics, but request shape differs by model family.

First-pass implementation target:

- current `providers/bedrock` path only
- prioritize Anthropic Claude-compatible `InvokeModel` request bodies already used by this repo
- map shared `CacheControl` where the current request body can express it safely

Deferred follow-up:

- Bedrock Converse API
- Nova `cachePoint` support
- broader system/tool cache checkpoint placement beyond the current provider shape

### Gemini notes

Gemini cache support exists as `cachedContents.create` and subsequent references to a cached resource. That is not the same as inline scoped cache control on a `generateContent` request.

This proposal explicitly defers Gemini support because it needs a separate resource lifecycle API rather than a simple `Part.CacheControl` mapping.

## Request Mapping Requirements

### OpenAI / OpenAI Responses / Azure

Required:

- parse returned cached token counters into `Usage.Cache.CachedInputTokens`
- add support for `prompt_cache_retention` in provider option allowlists and request mapping

Not supported:

- `Part.CacheControl`
- `Tool.CacheControl`

### Anthropic

Required:

- extend response usage parsing to include:
  - `cache_read_input_tokens`
  - `cache_creation_input_tokens`
  - `cache_creation`
- map `Part.CacheControl` to Anthropic content block `cache_control`
- map `Tool.CacheControl` to Anthropic tool `cache_control`
- support `TTL` values `""`, `"5m"`, and `"1h"`

Validation rules:

- reject unsupported TTL values
- reject cache control on roles or structures that cannot be represented in the Anthropic request body

### Bedrock

Required:

- extend response parsing to include Bedrock cache-read and cache-write metrics when present
- map `Part.CacheControl` onto the current Claude-style request body for user/assistant text parts
- support `TTL` values supported by the target Bedrock model family

Validation rules:

- reject unsupported TTL values
- reject scoped cache control for shapes the current Bedrock provider cannot express yet
  - current implementation rejects system-part cache control
  - current implementation rejects tool cache control

## Streaming Requirements

1. Final `StreamEvent.Usage` must carry the same cache breakdown as the blocking path.
2. No requirement to emit cache counters incrementally before the final stream event.
3. For providers whose streaming APIs only provide complete usage at the end, `uniai` should continue to populate cache stats only in the final event.

## Compatibility Notes

This feature extends exported structs (`Usage`, `Part`, `Tool`), which is a source-compatibility risk for callers using unkeyed composite literals across package boundaries.

This proposal accepts that risk because:

- the new capability needs first-class structured fields
- `Result.Raw` is not a sufficient unified API
- the repository already exposes these structs as evolving request and response models

Implementation notes:

- place new fields at the end of the structs
- update all in-repo composite literals to keyed form where needed
- mention the compatibility caveat in release notes and README updates

## Runnable Validation CLI

Add a new live-provider validation program under:

- `cmd/cachetest/main.go`
- `cmd/cachetest/README.md`
- `cmd/cachetest/env.example.sh`

### Purpose

Validate real cache behavior against live providers using environment-provided credentials and model settings.

This tool is required because cache behavior is upstream-controlled and cannot be fully validated by unit tests alone.

### Configuration source

The program must read configuration from environment variables, following the same style as the existing `cmd/` tools.

Common environment variables:

- `PROVIDER`
- `MODEL`
- `TIMEOUT_SECONDS`
- `CACHE_SCENE`
- `CACHE_TTL`
- `CACHE_STREAM`

Provider-specific credentials should reuse existing env variable conventions from other `cmd/` tools:

- OpenAI:
  - `OPENAI_API_KEY`
  - `OPENAI_API_BASE`
  - `OPENAI_MODEL`
- Azure:
  - `AZURE_OPENAI_API_KEY`
  - `AZURE_OPENAI_ENDPOINT`
  - `AZURE_OPENAI_DEPLOYMENT`
  - `AZURE_OPENAI_API_VERSION`
- Anthropic:
  - `ANTHROPIC_API_KEY`
  - `ANTHROPIC_MODEL`
- Bedrock:
  - `AWS_ACCESS_KEY_ID`
  - `AWS_SECRET_ACCESS_KEY`
  - `AWS_REGION`
  - `BEDROCK_MODEL_ARN`

### Scenes

The CLI should support at least these scenes:

#### `stats`

Goal:

- validate that repeated equivalent requests surface cache-hit counters

Behavior:

1. Send a long static-prefix request once to warm the cache.
2. Send the same request again.
3. Assert that the second request reports a positive cache-hit count when the provider and model support cache stats.

Acceptance rule:

- success if the second request returns `Usage.Cache.CachedInputTokens > 0`
- for best-effort providers, allow a small retry budget before failing

#### `scope`

Goal:

- validate that explicit cache boundaries preserve hits for the static prefix while allowing changes after the boundary

Behavior:

1. Build request A with a large reusable prefix and explicit cache boundary.
2. Build request B with the same reusable prefix and a different dynamic suffix after the boundary.
3. Assert that request B reports cache hits.
4. Build request C that changes content before the boundary.
5. Assert that request C has fewer cache-hit tokens than request B, or zero where the provider reports misses clearly.

This scene only applies to providers with scoped cache control support.

#### `stream`

Goal:

- validate that final streaming usage contains the same cache counters as blocking mode

Behavior:

1. Repeat the `stats` scenario using `WithOnStream`.
2. Assert that the final `StreamEvent.Usage.Cache` contains the same non-zero cache-hit signal expected from blocking mode.

### CLI output requirements

The program should:

- print a concise human-readable summary to stdout
- emit a machine-readable JSON summary to stdout or a file
- return non-zero exit codes on assertion failure

Recommended JSON fields:

```json
{
  "provider": "anthropic",
  "model": "claude-sonnet-4-5",
  "scene": "scope",
  "success": true,
  "attempts": 2,
  "blocking": true,
  "streaming": false,
  "usage_first": {
    "input_tokens": 0,
    "output_tokens": 0,
    "total_tokens": 0,
    "cache": {
      "cached_input_tokens": 0,
      "cache_creation_input_tokens": 0,
      "details": {}
    }
  },
  "usage_second": {
    "input_tokens": 0,
    "output_tokens": 0,
    "total_tokens": 0,
    "cache": {
      "cached_input_tokens": 0,
      "cache_creation_input_tokens": 0,
      "details": {}
    }
  }
}
```

## Test Requirements

### Unit tests

Required unit-test coverage:

- OpenAI Chat Completions response parsing reads `prompt_tokens_details.cached_tokens`
- OpenAI Responses response parsing reads `input_tokens_details.cached_tokens`
- Azure response parsing reads `prompt_tokens_details.cached_tokens`
- Anthropic response parsing reads:
  - `cache_read_input_tokens`
  - `cache_creation_input_tokens`
  - `cache_creation`
- Bedrock response parsing reads cache usage when present
- Anthropic request mapping adds `cache_control` to marked parts and tools
- Bedrock request mapping adds cache markers where supported
- unsupported providers reject explicit `CacheControl`
- final stream usage includes cache fields on providers that stream final usage

### Live validation

Required manual validation targets:

- `openai` or `openai_resp`: `stats`
- `azure`: `stats`
- `anthropic`: `stats`, `scope`, `stream`
- `bedrock`: at least one supported cache-capable model for `stats` and `scope`

## Acceptance Criteria

This proposal is complete when all of the following are true:

1. `resp.Usage.Cache` is populated on every supported provider path when upstream returns cache metrics.
2. final streaming usage exposes the same cache metrics as blocking mode.
3. callers can mark cache boundaries on Anthropic and supported Bedrock requests without dropping to raw provider payloads.
4. requests with explicit scoped cache control fail fast on unsupported providers.
5. OpenAI-family providers expose cache-hit counters and support both `prompt_cache_key` and `prompt_cache_retention` through provider options.
6. `cmd/cachetest` can validate real cache behavior from environment-based credentials.

## Follow-Up Work

- README updates documenting cache usage fields and scoped cache controls
- examples showing Anthropic cache boundaries and OpenAI prompt-cache-key usage
- Gemini `cachedContents` design proposal
- broader Bedrock cache support beyond the current provider shape
