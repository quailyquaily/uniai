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

## OpenAI-Family Cache Options

`openai`, `openai_resp`, and `azure` do not support shared explicit cache
boundaries through `WithPartCacheControl(...)`.

What they do support is provider-specific request options such as:

- `prompt_cache_key`
- `prompt_cache_retention`

Use provider options for that path. These examples use
`github.com/lyricat/goutils/structs` for `structs.JSONMap`:

```go
resp, err := client.Chat(ctx,
	uniai.WithProvider("openai_resp"),
	uniai.WithModel("gpt-5"),
	uniai.WithMessages(uniai.User("hello")),
	uniai.WithOpenAIOptions(structs.JSONMap{
		"prompt_cache_key":       "tenant-a:shared-prefix:v1",
		"prompt_cache_retention": "24h",
	}),
)
```

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

For these providers, `uniai` standardizes usage reporting, but it does not try to
emulate inline cache boundaries inside the message list.

For Chat Completions-compatible backends, `uniai` reads cache-hit usage from the
standard `prompt_tokens_details.cached_tokens` field. If a compatible backend
instead returns a top-level `usage.cached_tokens`, `uniai` uses that as a
fallback.

For streaming Chat Completions requests on the shared OpenAI-compatible path,
`uniai` enables `stream_options.include_usage=true` so the final stream event can
carry usage when the upstream backend supports it.

## Provider Support

Current support is:

- `anthropic`: cache stats + explicit cache control
- `bedrock`: cache stats + limited explicit cache control
- `openai`: cache-hit stats only, no shared explicit cache control
- `openai_resp`: cache-hit stats only, no shared explicit cache control
- `azure`: cache-hit stats only, no shared explicit cache control
- `gemini`: no current shared cache API in `uniai`
- `cloudflare`: no current cache feature mapping in `uniai`

## Failure Behavior

If you pass explicit `CacheControl` to a provider that does not support it,
`Chat()` returns an error instead of silently ignoring the request.

That currently applies to:

- `openai`
- `openai_resp`
- `azure`
- `gemini`
- `cloudflare`

## Notes

- `WithPartCacheControl(...)` requires a non-empty text part when used on text.
- `Usage.Cache` is best-effort and depends on the upstream provider returning
  cache metrics.
- Gemini `cachedContents` is a separate resource flow and is not part of the
  current shared `chat.Request` API.
- `cmd/cachetest` contains runnable live-provider checks for cache behavior.
