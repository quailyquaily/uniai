# uniai

`uniai` is a small Go client that unifies chat, embeddings, image generation, reranking, and classification across multiple providers. It wraps provider-specific clients and normalizes request/response types.

## Features

- Chat routing with OpenAI-compatible, Azure OpenAI, Anthropic, AWS Bedrock, and Susanoo providers.
- Streaming support via callback — same `Chat()` signature, opt-in with `WithOnStream`.
- Embedding, image, rerank, and classify helpers with provider-specific options.
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

1. `chat.WithProvider(...)`
2. `Config.Provider`
3. default: `"openai"`

Supported provider names:

- `openai` (default)
- `openai_custom` (uses `Config.OpenAIAPIBase`)
- `deepseek` (OpenAI-compatible)
- `xai` (OpenAI-compatible)
- `gemini` (OpenAI-compatible)
- `azure`
- `anthropic`
- `bedrock`
- `susanoo`

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
            // stream finished; ev.Usage contains token counts
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

Supported providers: OpenAI, Azure, Anthropic, Bedrock. Susanoo ignores streaming and falls back to blocking.

When combined with tool emulation (`WithToolsEmulationMode`), the internal decision request is always non-streaming; only the final text response streams.

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

- OpenAI/OpenAI-compatible: `OpenAIAPIKey`, `OpenAIAPIBase`, `OpenAIModel`
- Azure OpenAI: `AzureOpenAIAPIKey`, `AzureOpenAIEndpoint`, `AzureOpenAIModel`
- Anthropic: `AnthropicAPIKey`, `AnthropicModel`
- AWS Bedrock: `AwsKey`, `AwsSecret`, `AwsRegion`, `AwsBedrockModelArn`
- Susanoo: `SusanooAPIBase`, `SusanooAPIKey`
- Embeddings/Rerank/Classify (Jina): `JinaAPIKey`, `JinaAPIBase`
- Gemini: `GeminiAPIKey`, `GeminiAPIBase`

Example:

```go
client := uniai.New(uniai.Config{
    Provider:     "openai",
    OpenAIAPIKey: "...",
    OpenAIModel:  "gpt-5.2",
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

## Development

Run from the module root that contains `go.mod`:

```bash
go test ./...
go vet ./...
```
