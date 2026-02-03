# uniai

`uniai` is a small Go client that unifies chat, embeddings, image generation, reranking, and classification across multiple providers. It wraps provider-specific clients and normalizes request/response types.

## Features

- Chat routing with OpenAI-compatible, Azure OpenAI, Anthropic, AWS Bedrock, and Susanoo providers.
- Embedding, image, rerank, and classify helpers with provider-specific options.
- Optional OpenAI-compatible adapter to reuse `github.com/sashabaranov/go-openai` request types.

## Install

This package is intended to live in a Go module that provides `go.mod`.

```bash
go get github.com/quailyquaily/uniai
```

## Quick Start (Chat)

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

## Tool calling

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

If you already use `github.com/sashabaranov/go-openai`, you can reuse its request types:

```go
import (
    "context"

    "github.com/quailyquaily/uniai"
    uniaiopenai "github.com/quailyquaily/uniai/chat/openai"
    openai "github.com/sashabaranov/go-openai"
)

func example(ctx context.Context) error {
    base := uniai.New(uniai.Config{OpenAIAPIKey: "...", OpenAIModel: "gpt-5.2"})
    client := uniaiopenai.New(base)

    _, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
        Model: "gpt-5.2",
        Messages: []openai.ChatCompletionMessage{
            {Role: openai.ChatMessageRoleUser, Content: "hello"},
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

## Tool calling support

OpenAI-compatible and Azure providers map tools and tool choice. Anthropic and Bedrock currently return a warning in `Result.Warnings` when tools are provided.

## Development

Run from the module root that contains `go.mod`:

```bash
go test ./...
go vet ./...
```
