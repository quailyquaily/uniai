# uniai

`uniai` is a small Go client that unifies chat, embeddings, image generation, reranking, and classification across multiple providers. It wraps provider-specific clients and normalizes request/response types.

## Features

- Chat routing with OpenAI-compatible, Azure OpenAI, Anthropic, AWS Bedrock, and Susanoo providers.
- Embedding, image, rerank, and classify helpers with provider-specific options.
- Optional OpenAI-compatible adapter to reuse the official `github.com/openai/openai-go/v3` request types.

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
    uniai.WithToolsEmulation(true),
)
```

Behavior:

- The client always sends `tools` and `tool_choice` to the upstream provider first.
- If the upstream response contains `tool_calls`, they are returned as-is.
- If there are tools but no `tool_calls`, the uniai will try to choose a tool and generate a tool call based on the response text.

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
- Debug logging: `Debug` (prints request/response payloads for chat providers)

Example:

```go
client := uniai.New(uniai.Config{
    Provider:     "openai",
    OpenAIAPIKey: "...",
    OpenAIModel:  "gpt-5.2",
    Debug:        true,
})
```

## Development

Run from the module root that contains `go.mod`:

```bash
go test ./...
go vet ./...
```
