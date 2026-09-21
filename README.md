# uniai

[![Go Reference](https://pkg.go.dev/badge/github.com/quailyquaily/uniai.svg)](https://pkg.go.dev/github.com/quailyquaily/uniai)

`uniai` is a small Go client that unifies chat, embeddings, image generation, audio transcription, reranking, and classification across multiple providers. It wraps provider-specific clients and normalizes request/response types.

## Features

- Chat routing with OpenAI-compatible providers (OpenAI, DeepSeek, xAI, Groq, Meta Model API), OpenAI Responses and Codex, Sakana AI, Azure OpenAI, Anthropic, AWS Bedrock, and Cloudflare Workers AI.
- Multimodal chat input via `Message.Parts` (`text`, `image_url`, `image_base64`) with provider-aware validation.
- Streaming support via callback — same `Chat()` signature, opt-in with `WithOnStream`.
- Embedding, image, audio, rerank, and classify helpers with provider-specific options.
- Structured judgments with native TypeSafe/Jev and Chat emulation, including self-hosted Qwen (see [`docs/evaluate.md`](docs/evaluate.md)).
- Optional OpenAI-compatible adapter to reuse the official `github.com/openai/openai-go/v3` request types.
- Tool calling with emulation, to support models which do not natively support tool calling (see [`docs/tool_emulation.md`](docs/tool_emulation.md)).
- Prompt-cache usage reporting and explicit cache boundaries for supported providers (see [`docs/cache.md`](docs/cache.md)).

## Install

This package is intended to live in a Go module that provides `go.mod`.

```bash
go get github.com/quailyquaily/uniai
```

## Build the subscription proxy

Build the example proxy for Linux ARM64 from the repository root:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -trimpath -o subscriptionproxy-linux-arm64 ./cmd/subscriptionproxy
```

For 32-bit Linux ARMv7:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -trimpath -o subscriptionproxy-linux-armv7 ./cmd/subscriptionproxy
```

See [`cmd/subscriptionproxy/README.md`](cmd/subscriptionproxy/README.md) for native and macOS ARM64 builds, login, explicit token-file configuration, and server usage.

## Chat

```go
package main

import (
    "context"
    "log"

    "github.com/quailyquaily/uniai"
)

func main() {
    client := uniai.New(uniai.Config{
        Provider:     "openai",
        OpenAIAPIKey: "...",
        OpenAIModel:  "gpt-5.2",
    })

    resp, err := client.Chat(context.Background(),
        uniai.WithModel("gpt-5.2"),
        uniai.WithMessages(
	        uniai.System("You are a helpful assistant."),
	        uniai.User("Say hello."),
        ),
        uniai.WithTemperature(0.7),
    )
    if err != nil {
        log.Fatal(err)
    }

    log.Println(resp.Text)
}
```

### Provider selection

`Chat` chooses the provider in this order:

1. `uniai.WithProvider(...)`
2. `Config.Provider`
3. default: `"openai"`

Supported provider names:

- `openai` (default)
- `openai_resp` (native OpenAI Responses API)
- `openai_codex` (Responses API with Codex request compatibility)
- `deepseek` (OpenAI-compatible)
- `xai` (OpenAI-compatible)
- `groq` (OpenAI-compatible)
- `meta` (Meta Model API, OpenAI-compatible)
- `sakana` (Sakana AI Responses-compatible)
- `gemini` (native Gemini API)
- `azure`
- `anthropic`
- `claude_oauth` (Claude subscription with a Claude Code HTTP request profile)
- `bedrock`
- `cloudflare`

For custom OpenAI-compatible endpoints, use provider `openai` with `Config.OpenAIAPIBase`.

For custom Anthropic-compatible endpoints, use provider `anthropic` with `Config.AnthropicAPIBase`. Set it to the provider's Messages API base, for example `https://api.anthropic.com/v1`; uniai appends `/messages`.

### Claude subscriptions

Use `claude_oauth` with a caller-owned `subscription.CredentialSource`:

```go
client := uniai.New(uniai.Config{
    Provider:           "claude_oauth",
    AnthropicModel:     "claude-sonnet-4-6",
    ClaudeSubscription: source,
})
```

The source supplies the Claude OAuth access token and account ID, and serializes
refresh-token rotation. `subscription/claude` provides stateless PKCE login and
refresh helpers. The library does not read credential files or start a CLI.

`subscription/claude/claudecode` owns the HTTP compatibility profile: OAuth and
CLI headers, an added Claude Code identity system block, and account-bound
`metadata.user_id`. It preserves other system blocks and tool names, IDs, and
arguments. Optional `Config.ClaudeCode` fields select the version, device ID, and
conversation UUID; otherwise each Chat call gets a fresh session. This is not a
complete client fingerprint or billing-signature implementation.

The upstream URL is fixed. `AnthropicAPIKey` and `AnthropicAPIBase` are not used
by `claude_oauth`; a missing subscription source is an error. A 401 triggers one
refresh and retry; other upstream errors do not switch credentials or providers.
`SubscriptionHTTPClient` can supply a custom transport. Redirects are disabled
to keep credentials on the intended upstream.

The [subscription proxy](cmd/subscriptionproxy/README.md) implements credential
storage, browser login, automatic refresh, and OpenAI-compatible Chat Completions
for Claude, including streaming and tool calls. Claude does not support the
proxy's `/v1/responses` endpoint. Tests use a simulated upstream; live account
compatibility has not been verified. `Usage.Cost`, when present, is an API-price
estimate, not an additional subscription charge.

### `openai`, `openai_resp`, and `openai_codex`

Use `openai` when you want Chat Completions behavior or compatibility with OpenAI-like providers.

Use `openai_resp` when you want native OpenAI Responses API behavior.

Use `openai_codex` for a Responses-compatible Codex endpoint that rejects sampling, token-limit, and explicit prompt-cache fields.

Practical differences:

- `openai` uses `/v1/chat/completions`
- `openai_resp` uses `/v1/responses`
- `openai_codex` also uses `/v1/responses`, with the same response and streaming parser as `openai_resp`
- `openai` is the safer choice for Chat Completions-compatible endpoints such as DeepSeek, xAI, Groq, Meta, or custom compatible bases
- `openai_resp` is the right choice for Responses-only features such as `previous_response_id` and requesting reasoning summaries
- `openai_resp` is stricter about unsupported Chat Completions-only options such as `stop`, `presence_penalty`, and `frequency_penalty`
- `openai_codex` omits `temperature`, token limits, reasoning budget, and explicit prompt-cache settings; it preserves reasoning effort
- In JSON object mode, `openai_codex` sends `text.format.type=json_object` and adds a user input instruction containing `JSON` when the input messages do not contain it
- `sakana` uses Sakana's Responses-compatible endpoint through the same request path as `openai_resp`

Important GPT-5.4 edge case:

- `openai` can fail on `gpt-5.4` when function tools are combined with reasoning effort, returning a 400 like:
  `Function tools with reasoning_effort are not supported for gpt-5.4 in /v1/chat/completions. Please use /v1/responses instead.`
- `openai_resp` exists specifically to handle that native Responses path.

There is a runnable repro/demo for this in [`cmd/openairesptest`](cmd/openairesptest).

Current model compatibility (checked 2026-09-21):

- `gpt-6-astra`: use `openai_resp` for tools. `openai` rejects requests with
  tools before sending them. Both paths omit unsupported sampling and logprob
  fields, accept `low` through `max` reasoning effort, and reject `none` and
  `minimal`. Legacy cache retention maps to `prompt_cache_options.ttl="30m"`;
  explicit cache options take precedence. System cache breakpoints are supported.
  See [OpenAI's migration guide](https://developers.openai.com/api/docs/guides/latest-model).
- `claude-fable-5-1` and `claude-mythos-5-1`: tool choice supports `auto` and
  `none`; `required` and a named function return a local error. Function tool
  `Strict` values are forwarded. `WithReasoningDetails()` requests summarized
  thinking. See [Claude 5.1 API changes](https://platform.claude.com/docs/en/models/fable-5-1/whats-new-fable-5-1).
- `deepseek-flash`: selects V4.1 Flash on the `deepseek` provider, including
  image inputs and tools. `WithReasoningEffort(ReasoningEffortNone)` sends
  `thinking.type="disabled"` for this model and V4 models. Preserve assistant
  `ReasoningContent` when replaying tool conversations. See
  [DeepSeek's thinking guide](https://api-docs.deepseek.com/guides/thinking_mode).
- `gemini-3.8-flash`: supports `low`, `medium`, and `high` effort; `minimal`
  returns a local error. See [model details](https://ai.google.dev/gemini-api/docs/models/gemini-3.8-flash).

### Reasoning

Reasoning-related chat interfaces:

- `uniai.WithReasoningEffort(...)`
- `uniai.WithReasoningBudgetTokens(...)`
- `uniai.WithReasoningDetails()`
- `resp.Reasoning`

Available effort constants:

- `uniai.ReasoningEffortNone`
- `uniai.ReasoningEffortMinimal`
- `uniai.ReasoningEffortLow`
- `uniai.ReasoningEffortMedium`
- `uniai.ReasoningEffortHigh`
- `uniai.ReasoningEffortMax`
- `uniai.ReasoningEffortXHigh`

Behavior notes:

- If you do not call any reasoning interface, `uniai` does not send reasoning-related request fields.
- `WithReasoningEffort(...)` controls reasoning level when the selected provider/model supports effort-style controls.
- `WithReasoningBudgetTokens(...)` controls reasoning token budget when the selected provider/model supports budget-style controls.
- `WithReasoningDetails()` opts in to retrieving provider reasoning details into `resp.Reasoning`.
- Reasoning details are read from returned protocol fields without a model-name allowlist. A response without reasoning fields still succeeds and leaves `resp.Reasoning` empty. Effort and budget request mappings remain provider-specific.

Provider guidance:

- OpenAI Chat Completions (`openai`) and compatible endpoints: `WithReasoningDetails()` captures returned `reasoning_content`, including for custom model names. It does not add a reasoning request field or require the server to return reasoning.
- Azure: `WithReasoningDetails()` captures returned `reasoning_content` in blocking and streaming responses.
- OpenAI Responses (`openai_resp`): use `WithReasoningEffort(...)`. `WithReasoningDetails()` is supported. For `gpt-5.6-luna`, normalized reasoning details are provider-generated summaries, not raw chain-of-thought.
- OpenAI Codex (`openai_codex`): use `WithReasoningEffort(...)`; `WithReasoningBudgetTokens(...)` is ignored. Reasoning output and streaming use the `openai_resp` parser.
- Gemini 3.x: use `WithReasoningEffort(...)`.
- Gemini 2.5: use `WithReasoningBudgetTokens(...)`.
- Anthropic Claude Fable/Mythos 5.1, Sonnet 5, and Claude 4.6 adaptive-thinking models: use
  `WithReasoningEffort(...)`.
- Anthropic manual-thinking models: use `WithReasoningBudgetTokens(...)`.
- Gemini, Anthropic, and Bedrock also accept `WithReasoningDetails()` alone for custom model names. Gemini requests thought summaries; Anthropic and Bedrock capture returned thinking blocks without inventing a manual budget. Details may be absent if the server does not produce them.

Example:

```go
resp, err := client.Chat(ctx,
	uniai.WithProvider("gemini"),
	uniai.WithModel("gemini-2.5-pro"),
	uniai.WithMessages(uniai.User("Solve this step by step.")),
	uniai.WithReasoningBudgetTokens(4096),
	uniai.WithReasoningDetails(),
)
if err != nil {
	log.Fatal(err)
}
log.Println(resp.Text)
if resp.Reasoning != nil {
	log.Printf("reasoning summary: %+v", resp.Reasoning.Summary)
}
```

### Multimodal chat input (V1)

`uniai` supports structured chat content with `Message.Parts`.

Supported part types:

- `text`
- `image_url`
- `image_base64`

Role constraints:

- `user` can use `text`, `image_url`, and `image_base64`.
- `system` / `assistant` / `tool` are text-only.

Example:

```go
resp, err := client.Chat(ctx,
    uniai.WithProvider("openai"),
    uniai.WithModel("gpt-5.2"),
    uniai.WithMessages(
        uniai.UserParts(
            uniai.TextPart("Describe this image."),
            uniai.ImageURLPart("https://example.com/cat.png"),
        ),
    ),
)
if err != nil {
    log.Fatal(err)
}
log.Println(resp.Text)
```

With base64 image input:

```go
resp, err := client.Chat(ctx,
    uniai.WithProvider("openai"),
    uniai.WithModel("gpt-5.2"),
    uniai.WithMessages(
        uniai.UserParts(
            uniai.TextPart("What do you see?"),
            uniai.ImageBase64Part("image/png", base64PNG),
        ),
    ),
)
```

Behavior notes:

- `Parts` takes precedence over legacy `Content`.
- If `Parts` is empty and `Content` is set, `Content` is treated as one `text` part.
- `Result.Text` remains the compatibility field; `Result.Parts` is also populated (currently text parts in V1).
- Cloudflare native `messages` models such as `@cf/moonshotai/kimi-k2.5` support `image_url` and `image_base64`; the current `gpt-oss` responses-style path remains text-only.

Provider support details and examples: [`docs/multimodal_chat.md`](docs/multimodal_chat.md).

### Tool calling

```go
resp, err := client.Chat(ctx,
    uniai.WithModel("gpt-5.2"),
    uniai.WithMessages(uniai.User("What's the weather in Tokyo?")),
    uniai.WithTools([]uniai.Tool{
    uniai.FunctionTool("get_weather", "Get current weather", []byte(`{
            "type": "object",
            "properties": { "city": { "type": "string" } },
            "required": ["city"]
        }`)),
    }),
    uniai.WithToolChoice(uniai.ToolChoiceAuto()),
)
```

For multi-turn tool execution, preserve complete assistant replay messages from `resp.Messages`. `resp.ToolCalls` tells you which tools to run; it is not a lossless assistant history because some providers attach replay fields such as `reasoning_content` to the assistant message itself.

```go
messages := []uniai.Message{
	uniai.User("Use the echo tool to repeat: hello"),
}

resp, err := client.Chat(ctx,
	uniai.WithMessages(messages...),
	uniai.WithTools(tools),
)
if err != nil {
	return err
}

messages = append(messages, uniai.AssistantReplayMessages(resp)...)
for _, call := range resp.ToolCalls {
	messages = append(messages, uniai.ToolResult(call.ID, runTool(call)))
}
```

`AssistantReplayMessages` preserves provider-specific replay state such as Gemini thought signatures and OpenAI-compatible `reasoning_content`. Some providers need this state in follow-up tool rounds; see [`docs/workarounds.md`](docs/workarounds.md).

### Tool calling emulation

Some models may not support native tool calling. You can enable tools emulation with:

```go
resp, err := client.Chat(ctx,
    uniai.WithModel("your-model"),
    uniai.WithMessages(uniai.User("What's the weather in Tokyo?")),
    uniai.WithTools([]uniai.Tool{
        uniai.FunctionTool("get_weather", "Get current weather", []byte(`{
            "type": "object",
            "properties": { "city": { "type": "string" } },
            "required": ["city"]
        }`)),
        uniai.FunctionTool("get_direction", "Get a route from 2 addresses", []byte(`{
            "type": "object",
            "properties": { "address_from": { "type": "string" }, "address_to": { "type": "string" } },
            "required": ["address_from", "address_to"]
        }`)),
    }),
    uniai.WithToolChoice(uniai.ToolChoiceAuto()),
    uniai.WithToolsEmulationMode(uniai.ToolsEmulationForce),
)
```

See [`docs/tool_emulation.md`](docs/tool_emulation.md) for other emulation options and detailed behaviors.

### Streaming

Pass `WithOnStream` to receive tokens incrementally. The `Chat()` signature stays the same — it still returns the complete `Result` after the stream ends.

On providers that expose readable reasoning, combine it with `WithReasoningDetails()`. Reasoning continues through the same callback; there is no separate streaming method.

```go
resp, err := client.Chat(ctx,
    uniai.WithProvider("openai_resp"),
    uniai.WithModel("gpt-5.6-luna"),
    uniai.WithMessages(uniai.User("Tell me a story.")),
    uniai.WithReasoningEffort(uniai.ReasoningEffortHigh),
    uniai.WithReasoningDetails(),
    uniai.WithOnStream(func(ev uniai.StreamEvent) error {
        if ev.Done {
            // stream finished; ev.Usage contains final token counts and, when known, cost
            // ev.Raw contains provider-specific raw stream data when available
            return nil
        }
        if ev.ReasoningDelta != nil {
            fmt.Print(ev.ReasoningDelta.Delta) // provider-exposed reasoning text
        }
        if ev.Delta != "" {
            fmt.Print(ev.Delta) // incremental text
        }
        if ev.ToolCallDelta != nil {
            // incremental tool call (index, id, name, args chunk)
        }
        return nil // return non-nil error to cancel the stream
    }),
)
// resp.Text contains the full accumulated text
```

`StreamEvent` fields:

| Field | Description |
|---|---|
| `Delta` | Incremental text content |
| `ReasoningDelta` | Incremental provider-exposed reasoning (`Index`, `Type`, `Delta`) |
| `ToolCallDelta` | Incremental tool call update (`Index`, `ID`, `Name`, `ArgsChunk`) |
| `Usage` | Token usage, populated on the final event |
| `Raw` | Provider-specific raw stream event or raw stream response when available |
| `Done` | `true` for the last event |

`ReasoningDelta.Type` is `summary` for provider-labeled summaries and `thinking` for readable thinking or `reasoning_content`. Opaque signatures, encrypted content, and redacted data are kept out of this field.

Use the [stream reasoning test](cmd/stream/README.md) to verify live
`ReasoningDelta` events with API keys supplied through environment variables.

Text streaming is implemented for OpenAI (`openai`, `openai_resp`, `openai_codex`), OpenAI-compatible (`deepseek`, `xai`, `groq`, `meta`), Sakana (`sakana`), Azure, Anthropic, Gemini, and Bedrock. Cloudflare ignores streaming and falls back to blocking. This list does not mean that every provider or model exposes readable reasoning.

For OpenAI Chat Completions, Responses, Anthropic, and Gemini, stream errors and premature EOF return an error without a final `Done` event. Completion is checked against the protocol: Chat Completions accepts `[DONE]` or finish reasons for all received choices, Responses requires a terminal response, Anthropic requires `message_stop`, and Gemini requires a finish reason or prompt-block feedback. Chat Completions and Gemini preserve trailing usage chunks before the stream ends.

`Result.FinishReason` and the final event's `FinishReason` distinguish `stop`, `length`, `tool_calls`, and `content_filter` when the provider reports them. `Done` alone does not mean that the answer is complete: token limits and filtering can also end a response. Responses API `failed` and `incomplete` states continue to return errors. A compatible Chat Completions endpoint that sends `[DONE]` without a finish reason leaves `FinishReason` empty.

The bundled live reasoning test contains cases for DeepSeek V4 Pro, Kimi K3, Claude Sonnet 5, and GPT-5.6 Luna. A case passes only when its callback receives a non-empty `ReasoningDelta` and a final `Done` event.

For OpenAI Chat Completions streaming providers (`openai`, OpenAI-compatible providers, and Azure), `StreamEvent.Raw` is the current SDK chunk on delta events. On the final `Done` event, `StreamEvent.Raw` and the returned `Result.Raw` contain the complete `[]openai.ChatCompletionChunk` stream.

When combined with tool emulation (`WithToolsEmulationMode`), only the final text response streams. The final `Usage` / `Usage.Cost` values reflect the whole `Client.Chat()` call, including internal tool-emulation requests.

## Evaluate

Use the `evaluate` package to describe named Boolean, Choice, and Score questions over shared state:

```go
client := uniai.New(uniai.Config{
    TypeSafeAPIKey: "...",
    EvaluateModel: "jev-1.13.0",
})
result, err := client.Evaluate(ctx, evaluate.Request{
    State: "Please refund my duplicate payment.",
    Questions: map[string]evaluate.Question{
        "refund": {Kind: evaluate.Boolean, Instructions: "Is a refund requested?"},
    },
})
```

To run the same questions through a self-hosted OpenAI-compatible Chat service:

```go
client := uniai.New(uniai.Config{
    OpenAIAPIBase: qwenAPIBase,
    OpenAIAPIKey: qwenAPIKey,
    EvaluateProvider: "openai",
    EvaluateModel: qwenModel,
    EvaluateEmulationMode: evaluate.EmulationForce,
})
```

`off` requires native Evaluate; `fallback` selects Chat only when the chosen provider has no native path; `force` selects Chat directly. Native errors never trigger Chat fallback. Jev returns `ProbabilityTrue`; emulation returns `BooleanValue` without invented probabilities. See [Evaluate usage and generation controls](docs/evaluate.md).

Use [`cmd/evalbench`](cmd/evalbench/README.md) to benchmark latency and judgments with credentials from environment variables. It includes 614 labeled cases across 18 scenarios, balanced Chinese and Japanese subsets, per-call answers, latency percentiles, and JSON reports:

```bash
go run ./cmd/evalbench --dry-run
go run ./cmd/evalbench --limit 18
```

## Cost estimation

`uniai` ships an embedded default pricing catalog for common chat, image generation, and native Evaluate models. Evaluate emulation uses the actual Chat model's pricing.

By default, `uniai` fills `Usage.Cost` on blocking chat results, final chat streaming events, and image generation results when the current model matches the embedded catalog. Under tool emulation, `Usage` and `Usage.Cost` are aggregated across the internal chat requests used to satisfy the single `Client.Chat()` call.

For models with long-context pricing tiers, `uniai` selects the tier from each upstream request's raw `input_tokens` count before any tool-emulation aggregation happens.

The embedded default catalog is sourced from `pricing.example.yaml`.

Set `Config.Pricing` to override the default catalog. Pass `&uniai.PricingCatalog{}` if you want to disable automatic cost estimation.

If the same model name appears under multiple `inference_provider` values in your catalog, pass `uniai.WithInferenceProvider(...)` on the request to select the intended rule. If you do not pass it, pricing falls back to model-only matching.

Detailed usage notes live in [`docs/pricing.md`](docs/pricing.md).

```go
client := uniai.New(uniai.Config{
    Provider: "openai",
})

resp, err := client.Chat(ctx,
    uniai.WithModel("gpt-5.4"),
    uniai.WithInferenceProvider("openai"),
    uniai.WithMessages(uniai.User("hello")),
)
if err != nil {
    log.Fatal(err)
}
if resp.Usage.Cost != nil {
    log.Printf("estimated cost: %s %.8f", resp.Usage.Cost.Currency, resp.Usage.Cost.Total)
}
```

To override the default catalog, parse YAML or build one directly in Go:

```go
pricing, err := uniai.ParsePricingYAML(yamlBytes)
if err != nil {
    log.Fatal(err)
}

client := uniai.New(uniai.Config{
	Provider: "openai",
	Pricing:  pricing,
})
```

To disable automatic cost estimation explicitly:

```go
client := uniai.New(uniai.Config{
	Provider: "openai",
	Pricing:  &uniai.PricingCatalog{},
})
```

`Usage.Cost` is a local estimate derived from token counts and the active price table. It is not a verbatim upstream billing record.

## Embeddings

```go
emb, err := client.Embedding(ctx,
    uniai.Embedding("text-embedding-3-small", "hello"),
)
```

## Images

```go
img, err := client.Image(ctx,
    uniai.Image("gpt-image-2", "a minimal line-art cat"),
    uniai.WithCount(1),
)
```

Image generation, editing, supported models, and provider-specific options are in [docs/images.md](docs/images.md).

## Audio (ASR)

The audio API supports Cloudflare transcription. GPT-Live and Gemini Live
realtime sessions are not implemented.

```go
resp, err := client.Audio(ctx,
    uniai.Audio("@cf/openai/whisper-large-v3-turbo", base64Audio),
)
```

## Rerank

```go
resp, err := client.Rerank(ctx,
    uniai.Rerank("jina-reranker", "what is uniai?",
    uniai.RerankInput{Text: "..."},
    uniai.RerankInput{Text: "..."},
    ),
    uniai.WithTopN(5),
    uniai.WithReturnDocuments(true),
)
```

## Classify

```go
resp, err := client.Classify(ctx,
    uniai.Classify("jina-classifier", []string{"billing", "support"},
    uniai.ClassifyInput{Text: "I need a refund"},
    ),
)
```

## OpenAI-compatible adapter

If you already use the official OpenAI Go SDK (`github.com/openai/openai-go/v3`), you can reuse its request types:

```go
import (
    "context"

    "github.com/quailyquaily/uniai"
    openai "github.com/openai/openai-go/v3"
    uniaiopenai "github.com/quailyquaily/uniai/chat/openai"
)

func example(ctx context.Context) error {
    base := uniai.New(uniai.Config{OpenAIAPIKey: "...", OpenAIModel: "gpt-5.2"})
    client := uniaiopenai.New(base)

    _, err := client.CreateChatCompletion(ctx, openai.ChatCompletionNewParams{
        Model: openai.ChatModel("gpt-5.2"),
        Messages: []openai.ChatCompletionMessageParamUnion{
            openai.UserMessage("hello"),
        },
    })
    return err
}
```

## Configuration

All configuration is provided via `uniai.Config`. Only the fields required for the providers you use need to be set.

- Chat defaults: `Provider`, `Debug`, `ChatHeaders`, `Pricing` (`ChatHeaders` apply to chat provider HTTP requests only; `Pricing` overrides the embedded default pricing catalog used for `Usage.Cost`)
- OpenAI/OpenAI-compatible: `OpenAIAPIKey`, `OpenAIAPIBase`, `OpenAIModel`
- Meta Model API: use `Provider: "meta"` with `OpenAIAPIKey`, `OpenAIModel`, and optional `OpenAIAPIBase` override. The built-in base is `https://api.ai.meta.com/v1`.
- Sakana AI: use `Provider: "sakana"` with `OpenAIAPIKey`, `OpenAIModel`, and optional `OpenAIAPIBase` override
- Azure OpenAI: `AzureOpenAIAPIKey`, `AzureOpenAIEndpoint`, `AzureOpenAIModel`
- Anthropic: `AnthropicAPIKey`, `AnthropicAPIBase`, `AnthropicModel`
- AWS Bedrock: `AwsKey`, `AwsSecret`, `AwsRegion`, `AwsBedrockModelArn`
- Cloudflare Workers AI: `CloudflareAccountID`, `CloudflareAPIToken`, `CloudflareAPIBase`
- Embeddings/Rerank/Classify (Jina): `JinaAPIKey`, `JinaAPIBase`
- Gemini: `GeminiAPIKey`, `GeminiAPIBase`

Example:

```go
client := uniai.New(uniai.Config{
    Provider:     "openai",
    OpenAIAPIKey: "...",
    OpenAIModel:  "gpt-5.2",
    ChatHeaders: map[string]string{
        "X-Request-ID": "req-123",
    },
    Debug:        true,
})
```

## Debug logging

### Global debug

Set `Config.Debug` to `true` to enable request/response logging for all calls:

```go
client := uniai.New(uniai.Config{
    Provider:     "openai",
    OpenAIAPIKey: "...",
    OpenAIModel:  "gpt-5.2",
    Debug:        true,
})
```

If you want to capture request/response payloads without logging, use `WithDebugFn`:

`WithDebugFn` overrides `Config.Debug`: when set, logs are suppressed and all debug output is sent to the callback.
On request failures, providers also forward error payloads (raw API error body when available) through the same `*.response` label. When provider SDKs expose raw HTTP response text, an extra `*.response.raw_text` callback is emitted.


```go
resp, err := client.Chat(ctx,
    uniai.WithModel("gpt-5.2"),
    uniai.WithMessages(uniai.User("hello")),
    uniai.WithDebugFn(func(label, payload string) {
        // handle debug payloads (request/response)
        // - label: "{provider}.{function}.{request|response}"
        // 	 - e.g. "openai.chat.request", "anthropic.chat.response"
        // - payload: the content of the request/response
        // store them, send to external logger, etc.
    }),
)
```

## Testcase

Run tests from the module root that contains `go.mod`.

```bash
# all tests
GOCACHE=/tmp/go-build go test ./...

# only integration tests (chat + other features)
GOCACHE=/tmp/go-build go test ./... -run TestChatEchoJSON
GOCACHE=/tmp/go-build go test ./... -run TestOtherFeatures
```

Integration tests are enabled by env vars. Common ones:

- Chat: `TEST_OPENAI_API_KEY`, `TEST_OPENAI_MODEL`, `TEST_OPENAI_API_BASE`, `TEST_GROQ_API_KEY`, `TEST_GROQ_MODEL`, `TEST_META_API_KEY`, `TEST_META_MODEL`, `TEST_META_API_BASE`, `TEST_SAKANA_API_KEY`, `TEST_SAKANA_MODEL`
- Cloudflare chat/audio: `TEST_CLOUDFLARE_ACCOUNT_ID`, `TEST_CLOUDFLARE_API_TOKEN`, `TEST_CLOUDFLARE_TEXT_MODEL`, `TEST_CLOUDFLARE_AUDIO_MODEL`, `TEST_CLOUDFLARE_AUDIO_FILEPATH`, `TEST_CLOUDFLARE_API_BASE`
- Embedding/image/rerank/classify: see `env.example.sh`

## Development

Run from the module root that contains `go.mod`:

```bash
go test ./...
go vet ./...
```
