# Prompt Caching

This document describes the current prompt-caching behavior in `uniai`.

It covers two separate things:

- reading cache usage from `Result.Usage`
- explicitly marking cache boundaries when the upstream provider supports it

## What `uniai` Exposes

`uniai` exposes cache usage through `Usage.Cache`:

- `CachedInputTokens`: input tokens served from cache
- `CacheCreationInputTokens`: input tokens written into cache
- `Details`: provider-specific cache breakdowns such as Anthropic TTL buckets

These fields are additional breakdown data. They do not replace `InputTokens` or
`TotalTokens`.

Blocking `Chat()` responses and the final streaming event use the same cache
fields.

## Main API

Shared cache-control helpers:

- `uniai.WithPartCacheControl(...)`
- `uniai.WithToolCacheControl(...)`
- `uniai.CacheTTL5m()`
- `uniai.CacheTTL1h()`

The shared `CacheControl` shape is intentionally small:

```go
type CacheControl struct {
	TTL string
}
```

Currently supported TTL values are:

- `""` for provider default
- `"5m"`
- `"1h"`

### Provider Differences

`WithPartCacheControl(...)` gives providers a shared way to mark a cache
boundary. It does not make their cache policies identical.

| Provider path | Shared boundary support | Value passed to `WithPartCacheControl` | TTL location |
| --- | --- | --- | --- |
| `anthropic` | system, user, and assistant parts; tools | `CacheTTL5m()` or `CacheTTL1h()` | each marked part or tool |
| Anthropic models through `bedrock` | user and assistant text parts | `CacheTTL5m()` or `CacheTTL1h()` | each marked part |
| GPT-5.6 through `openai` | system text parts | `CacheControl{}` | request-wide; defaults to `30m` |
| GPT-5.6 through `openai_resp` | system text parts; additional shapes through raw `input` | `CacheControl{}` for shared system parts | request-wide; defaults to `30m` |
| `openai_codex` | none; shared cache controls are ignored | n/a | n/a |

Do not pass `CacheTTL5m()` or `CacheTTL1h()` to a GPT-5.6 breakpoint. OpenAI
uses the non-nil, empty `CacheControl{}` only as a boundary marker and rejects a
part-level TTL. Its request-wide TTL defaults to `30m`. Anthropic and Bedrock
use the TTL stored in `CacheControl`.

## Reading Cache Usage

Example:

```go
resp, err := client.Chat(ctx,
	uniai.WithProvider("anthropic"),
	uniai.WithModel("claude-sonnet-4-6"),
	uniai.WithMessages(uniai.User("hello")),
)
if err != nil {
	return err
}

fmt.Printf("cached=%d cache_write=%d details=%v\n",
	resp.Usage.Cache.CachedInputTokens,
	resp.Usage.Cache.CacheCreationInputTokens,
	resp.Usage.Cache.Details,
)
```

If you also enable pricing, `Usage.Cost` can include cached-input and cache-write
costs when the matched pricing rule defines those rates.

## Explicit Cache Control

Explicit cache control means placing a cache checkpoint on a message part or tool
definition.

This is supported only on providers that can express inline cache boundaries.

### Anthropic

Anthropic supports explicit cache control on:

- system text parts
- user and assistant parts
- tools

Example:

```go
resp, err := client.Chat(ctx,
	uniai.WithProvider("anthropic"),
	uniai.WithModel("claude-sonnet-4-6"),
	uniai.WithMessages(
		uniai.SystemParts(
			uniai.WithPartCacheControl(
				uniai.TextPart("Shared instructions and reusable context."),
				uniai.CacheTTL1h(),
			),
		),
		uniai.UserParts(
			uniai.TextPart("Answer this question using the shared context."),
		),
	),
	uniai.WithTools([]uniai.Tool{
		uniai.WithToolCacheControl(
			uniai.FunctionTool("lookup_docs", "Search internal docs", []byte(`{"type":"object"}`)),
			uniai.CacheTTL5m(),
		),
	}),
)
```

Anthropic writes the TTL directly on each marked block:

```json
{
  "type": "text",
  "text": "Shared instructions and reusable context.",
  "cache_control": {
    "type": "ephemeral",
    "ttl": "1h"
  }
}
```

No additional request-level cache policy is required for this explicit marker.

### Bedrock

Bedrock currently supports explicit cache control only in the current Anthropic
Claude request path, and only on user or assistant text parts.

It does not currently support explicit cache control on:

- system parts
- tools
- non-Anthropic Bedrock model ARNs

Example:

```go
resp, err := client.Chat(ctx,
	uniai.WithProvider("bedrock"),
	uniai.WithModel("arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-sonnet-4-20250514-v1:0"),
	uniai.WithMessages(
		uniai.UserParts(
			uniai.WithPartCacheControl(
				uniai.TextPart("Reusable prefix."),
				uniai.CacheTTL5m(),
			),
			uniai.TextPart("Request-specific suffix."),
		),
	),
)
```

## OpenAI-Family Caching

`openai`, `openai_resp`, and `azure` support provider-specific root cache
options. GPT-5.6 through `openai` and `openai_resp` also supports explicit
breakpoints through the shared message API. Adding a breakpoint does not require
root cache options.

`openai_codex` deliberately disables explicit cache configuration. It omits
`prompt_cache_key`, `prompt_cache_retention`, and `prompt_cache_options`, removes
`prompt_cache_breakpoint` from raw input, and ignores shared cache controls.
Upstream implicit caching may still apply, and returned cache usage is parsed as
usual.

### GPT-5.6 Explicit Breakpoints

For `openai` and `openai_resp`, a shared cache boundary can be placed on a
GPT-5.6 system text part. Only the marked system message changes from string
content to structured parts; unmarked messages keep their existing form.

```go
resp, err := client.Chat(ctx,
	uniai.WithProvider("openai"),
	uniai.WithModel("gpt-5.6"),
	uniai.WithMessages(
		uniai.SystemParts(
			uniai.WithPartCacheControl(
				uniai.TextPart("Shared instructions and reusable context."),
				uniai.CacheControl{},
			),
			uniai.TextPart("Request-specific system suffix."),
		),
		uniai.User("Answer using the shared context."),
	),
)
```

For Chat Completions, `uniai` serializes the marked system message as:

```json
{
  "role": "system",
  "content": [
    {
      "type": "text",
      "text": "Shared instructions and reusable context.",
      "prompt_cache_breakpoint": {
        "mode": "explicit"
      }
    },
    {
      "type": "text",
      "text": "Request-specific system suffix."
    }
  ]
}
```

The breakpoint includes the marked block and everything before it in the
reusable prefix. Content after it may change. The empty shared `CacheControl`
marks only this boundary; its `TTL` field must remain empty for OpenAI.

GPT-5.6 uses implicit mode by default. In that mode, OpenAI places an automatic
breakpoint on the latest message and also uses the explicit breakpoint above.
Adding an explicit breakpoint is therefore separate from setting the request
mode to `"explicit"`.

Shared OpenAI breakpoints are limited to system text parts. User and assistant
messages and tools cannot use shared `CacheControl` on this path.

### GPT-5.6 Request Policy

Use `WithOpenAIOptions(...)` only when the request needs OpenAI-specific cache
policy, such as:

- a stable `prompt_cache_key`
- `mode: "explicit"` to disable the automatic implicit breakpoint
- an explicit `ttl` value

These examples use `github.com/lyricat/goutils/structs` for `structs.JSONMap`.
Add the following option to the preceding request when it should use only the
breakpoints supplied by the caller:

```go
uniai.WithOpenAIOptions(structs.JSONMap{
	"prompt_cache_key": "tenant-a:shared-prefix:v1",
	"prompt_cache_options": map[string]any{
		"mode": "explicit",
		"ttl":  "30m",
	},
})
```

For GPT-5.6, `prompt_cache_options` accepts:

- `mode`: `"implicit"` or `"explicit"`
- `ttl`: `"30m"`, currently the only supported value and also the default

In explicit mode, provide at least one breakpoint. Without one, the request does
not use prompt caching. Invalid option values return an error before the request
is sent.

### Raw Responses Breakpoints

Callers that need other Responses API input shapes can use raw `input` through
`WithOpenAIOptions(...)` on `openai_resp`:

```go
resp, err := client.Chat(ctx,
	uniai.WithProvider("openai_resp"),
	uniai.WithModel("gpt-5.6"),
	uniai.WithOpenAIOptions(structs.JSONMap{
		"input": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "input_text",
						"text": "Shared instructions and reusable context.",
						"prompt_cache_breakpoint": map[string]any{
							"mode": "explicit",
						},
					},
				},
			},
		},
	}),
)
```

Raw `openai.input` cannot be combined with shared chat messages. A
`prompt_cache_breakpoint` accepts only `mode: "explicit"`. The request-wide
policy remains optional and can be added as described above.

### GPT-5.6 Legacy Option Compatibility

For `gpt-5.6` and its model variants, the `openai` and `openai_resp`
providers convert a non-empty legacy `prompt_cache_retention` value to:

```json
{
  "prompt_cache_options": {
    "ttl": "30m"
  }
}
```

The legacy field is removed. If both fields are present, the explicit
`prompt_cache_options` value wins.

This conversion is necessarily lossy. `prompt_cache_retention` describes a
maximum retention policy, while `prompt_cache_options.ttl` describes a minimum
cache lifetime. The new API currently provides no direct one-to-one mapping and
accepts only `30m` for `ttl`.

GPT-5.5 keeps the legacy behavior: when a cache key or retention option is
present, `uniai` sends `prompt_cache_retention: "24h"`. Older models keep their
existing option mapping.

The optional OpenAI-compatible adapter in `chat/openai` also preserves
`PromptCacheRetention` and `PromptCacheOptions` from the official OpenAI Go SDK
request type.

### Azure

Azure uses the same idea through Azure options:

```go
resp, err := client.Chat(ctx,
	uniai.WithProvider("azure"),
	uniai.WithModel("my-deployment"),
	uniai.WithMessages(uniai.User("hello")),
	uniai.WithAzureOptions(structs.JSONMap{
		"prompt_cache_key":       "tenant-a:shared-prefix:v1",
		"prompt_cache_retention": "24h",
	}),
)
```

Azure forwards these provider options to the selected deployment. It does not
apply the GPT-5.6 legacy conversion because Azure deployment names do not
reliably identify the underlying model. Support for each option therefore
depends on the Azure API version and deployment.

### Usage Mapping

For these providers, `uniai` standardizes usage reporting. Apart from the
GPT-5.6 system text mapping described above, it does not infer or emulate inline
cache boundaries inside the message list.

For Chat Completions-compatible backends, `uniai` reads cache-hit usage from the
standard `prompt_tokens_details.cached_tokens` field. If a compatible backend
instead returns a top-level `usage.cached_tokens`, `uniai` uses that as a
fallback. It reads cache-write usage from
`prompt_tokens_details.cache_write_tokens` when the upstream response includes
that field.

For the OpenAI Responses API, `uniai` reads the corresponding cache-hit and
cache-write counters from `input_tokens_details.cached_tokens` and
`input_tokens_details.cache_write_tokens`.

For streaming Chat Completions requests on the shared OpenAI-compatible path,
`uniai` enables `stream_options.include_usage=true` so the final stream event can
carry usage when the upstream backend supports it.

## Provider Support

Current support is:

- `anthropic`: cache stats + explicit cache control
- `bedrock`: cache stats + limited explicit cache control
- `openai`: cache stats + root cache options + GPT-5.6 system text breakpoints
- `openai_resp`: cache stats + root cache options + GPT-5.6 system text and raw
  Responses breakpoints
- `azure`: cache stats + backend-dependent provider options, no shared explicit
  cache control
- `gemini`: no current shared cache API in `uniai`
- `cloudflare`: no current cache feature mapping in `uniai`

## Failure Behavior

If you pass explicit `CacheControl` where a provider does not support it,
`Chat()` returns an error instead of silently ignoring the request. GPT-5.6 on
`openai` and `openai_resp` accepts it only on system text parts. Earlier OpenAI
models reject all shared explicit cache control.

Unsupported shared cache control currently includes:

- `azure`
- `gemini`
- `cloudflare`
- user or assistant parts and tools on `openai` and `openai_resp`

## Notes

- `WithPartCacheControl(...)` requires a non-empty text part when used on text.
- OpenAI GPT-5.6 breakpoint lifetime is request-wide and defaults to `30m`. Do
  not set it through the shared `CacheControl.TTL` field.
- `Usage.Cache` is best-effort and depends on the upstream provider returning
  cache metrics.
- Gemini `cachedContents` is a separate resource flow and is not part of the
  current shared `chat.Request` API.
- `cmd/cachetest` contains runnable live-provider checks for cache behavior.
