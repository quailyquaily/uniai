# uniai

[![Go Reference](https://pkg.go.dev/badge/github.com/quailyquaily/uniai.svg)](https://pkg.go.dev/github.com/quailyquaily/uniai)

`uniai` is a small Go client that unifies chat, embeddings, image generation, audio transcription, reranking, and classification across multiple providers. It wraps provider-specific clients and normalizes request/response types.

## Features

- Chat routing with OpenAI-compatible providers (OpenAI, DeepSeek, xAI, Groq), Azure OpenAI, Anthropic, AWS Bedrock, and Cloudflare Workers AI.
- Multimodal chat input via `Message.Parts` (`text`, `image_url`, `image_base64`) with provider-aware validation.
- Streaming support via callback — same `Chat()` signature, opt-in with `WithOnStream`.
- Embedding, image, audio, rerank, and classify helpers with provider-specific options.
- Optional OpenAI-compatible adapter to reuse the official `github.com/openai/openai-go/v3` request types.
- Tool calling with emulation, to support models which do not natively support tool calling (see [`docs/tool_emulation.md`](docs/tool_emulation.md)).

## Install

This package is intended to live in a Go module that provides `go.mod`.

```bash
go get github.com/quailyquaily/uniai
```

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
- `deepseek` (OpenAI-compatible)
- `xai` (OpenAI-compatible)
- `groq` (OpenAI-compatible)
- `gemini` (native Gemini API)
- `azure`
- `anthropic`
- `bedrock`
- `cloudflare`

For custom OpenAI-compatible endpoints, use provider `openai` with `Config.OpenAIAPIBase`.

### `openai` vs `openai_resp`

Use `openai` when you want Chat Completions behavior or compatibility with OpenAI-like providers.

Use `openai_resp` when you want native OpenAI Responses API behavior.

Practical differences:

- `openai` uses `/v1/chat/completions`
- `openai_resp` uses `/v1/responses`
- `openai` is the safer choice for OpenAI-compatible endpoints such as DeepSeek, xAI, Groq, or custom compatible bases
- `openai_resp` is the right choice for current OpenAI-only features such as `previous_response_id` and `WithReasoningDetails()`
- `openai_resp` is stricter about unsupported Chat Completions-only options such as `stop`, `presence_penalty`, and `frequency_penalty`

Important GPT-5.4 edge case:

- `openai` can fail on `gpt-5.4` when function tools are combined with reasoning effort, returning a 400 like:
  `Function tools with reasoning_effort are not supported for gpt-5.4 in /v1/chat/completions. Please use /v1/responses instead.`
- `openai_resp` exists specifically to handle that native Responses path.

There is a runnable repro/demo for this in [`cmd/openairesptest`](cmd/openairesptest).

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

Provider guidance:

- OpenAI Chat Completions (`openai`): use `WithReasoningEffort(...)`. `WithReasoningDetails()` is not supported on this path.
- OpenAI Responses (`openai_resp`): use `WithReasoningEffort(...)`. `WithReasoningDetails()` is supported.
- Gemini 3.x: use `WithReasoningEffort(...)`.
- Gemini 2.5: use `WithReasoningBudgetTokens(...)`.
- Anthropic Claude 4.6: use `WithReasoningEffort(...)`.
- Anthropic manual-thinking models: use `WithReasoningBudgetTokens(...)`.

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

For multi-turn tool execution, always preserve `resp.ToolCalls` exactly as returned by `Chat`:

```go
messages := []uniai.Message{
	uniai.User("Use the echo tool to repeat: hello"),
}

Gemini needs follow-up tool rounds to preserve provider-specific metadata, please see [`docs/workarounds.md`](docs/workarounds.md).

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

```go
resp, err := client.Chat(ctx,
    uniai.WithModel("gpt-5.2"),
    uniai.WithMessages(uniai.User("Tell me a story.")),
    uniai.WithOnStream(func(ev uniai.StreamEvent) error {
        if ev.Done {
            // stream finished; ev.Usage contains final token counts and, when known, cost
            return nil
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
| `ToolCallDelta` | Incremental tool call update (`Index`, `ID`, `Name`, `ArgsChunk`) |
| `Usage` | Token usage, populated on the final event |
| `Done` | `true` for the last event |

Check out the [stream demo](cmd/stream/README.md) for a runnable terminal example.

Supported providers: OpenAI (`openai`, `openai_resp`), OpenAI-compatible (`deepseek`, `xai`, `groq`), Azure, Anthropic, Bedrock. Cloudflare ignores streaming and falls back to blocking.

When combined with tool emulation (`WithToolsEmulationMode`), only the final text response streams. The final `Usage` / `Usage.Cost` values reflect the whole `Client.Chat()` call, including internal tool-emulation requests.

## Cost estimation

`uniai` does not ship built-in model pricing.

If you provide a pricing catalog via `Config.Pricing`, `uniai` fills `Usage.Cost` on the blocking result and on the final streaming event when a rule matches the current provider/model. Under tool emulation, `Usage` and `Usage.Cost` are aggregated across the internal chat requests used to satisfy the single `Client.Chat()` call.

A maintained example catalog lives in `pricing.example.yaml`.

Detailed usage notes live in [`docs/pricing.md`](docs/pricing.md).

```go
pricing, err := uniai.ParsePricingYAML([]byte(`
version: uniai.pricing.v1
chat:
  - provider: openai
    model: gpt-5.4
    input_usd_per_million: 2.50
    output_usd_per_million: 15
    cached_input_usd_per_million: 0.25
`))
if err != nil {
    log.Fatal(err)
}

client := uniai.New(uniai.Config{
    Provider: "openai",
    Pricing:  pricing,
})

resp, err := client.Chat(ctx,
    uniai.WithModel("gpt-5.4"),
    uniai.WithMessages(uniai.User("hello")),
)
if err != nil {
    log.Fatal(err)
}
if resp.Usage.Cost != nil {
    log.Printf("estimated cost: %s %.8f", resp.Usage.Cost.Currency, resp.Usage.Cost.Total)
}
```

You can also build the catalog directly in Go:

```go
pricing := &uniai.PricingCatalog{
    Version: uniai.PricingCatalogVersionV1,
    Chat: []uniai.ChatPricingRule{
        {
            Provider:            "azure",
            Model:               "my-gpt-5-deployment",
            InputUSDPerMillion:  1.75,
            OutputUSDPerMillion: 14,
        },
    },
}
```

`Usage.Cost` is a local estimate derived from token counts and your price table. It is not a verbatim upstream billing record.

## Embeddings

```go
emb, err := client.Embedding(ctx,
    uniai.Embedding("text-embedding-3-small", "hello"),
)
```

## Images

```go
img, err := client.Image(ctx,
    uniai.Image("gpt-image-1", "a minimal line-art cat"),
    uniai.WithCount(1),
)
```

## Audio (ASR)

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

- Chat defaults: `Provider`, `Debug`, `ChatHeaders`, `Pricing` (`ChatHeaders` apply to chat provider HTTP requests only; `Pricing` is an optional external pricing catalog for `Usage.Cost`)
- OpenAI/OpenAI-compatible: `OpenAIAPIKey`, `OpenAIAPIBase`, `OpenAIModel`
- Azure OpenAI: `AzureOpenAIAPIKey`, `AzureOpenAIEndpoint`, `AzureOpenAIModel`
- Anthropic: `AnthropicAPIKey`, `AnthropicModel`
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

- Chat: `TEST_OPENAI_API_KEY`, `TEST_OPENAI_MODEL`, `TEST_OPENAI_API_BASE`, `TEST_GROQ_API_KEY`, `TEST_GROQ_MODEL`
- Cloudflare chat/audio: `TEST_CLOUDFLARE_ACCOUNT_ID`, `TEST_CLOUDFLARE_API_TOKEN`, `TEST_CLOUDFLARE_TEXT_MODEL`, `TEST_CLOUDFLARE_AUDIO_MODEL`, `TEST_CLOUDFLARE_AUDIO_FILEPATH`, `TEST_CLOUDFLARE_API_BASE`
- Embedding/image/rerank/classify: see `env.example.sh`

## Development

Run from the module root that contains `go.mod`:

```bash
go test ./...
go vet ./...
```
