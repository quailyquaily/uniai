# uniai

`uniai` is a small Go client that unifies chat, embeddings, image generation, audio transcription, reranking, and classification across multiple providers. It wraps provider-specific clients and normalizes request/response types.

## Features

- Chat routing with OpenAI-compatible providers (OpenAI, DeepSeek, xAI, Groq), Azure OpenAI, Anthropic, AWS Bedrock, Susanoo, and Cloudflare Workers AI.
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

1. `chat.WithProvider(...)`
2. `Config.Provider`
3. default: `"openai"`

Supported provider names:

- `openai` (default)
- `openai_custom` (uses `Config.OpenAIAPIBase`)
- `deepseek` (OpenAI-compatible)
- `xai` (OpenAI-compatible)
- `groq` (OpenAI-compatible)
- `gemini` (native Gemini API)
- `azure`
- `anthropic`
- `bedrock`
- `susanoo`
- `cloudflare`

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

tools := []uniai.Tool{
	uniai.FunctionTool("echo", "Echo text back", []byte(`{
		"type":"object",
		"properties":{"text":{"type":"string"}},
		"required":["text"]
	}`)),
}

for {
	resp, err := client.Chat(ctx,
		uniai.WithModel("gemini-2.5-pro"),
		uniai.WithReplaceMessages(messages...),
		uniai.WithTools(tools),
		uniai.WithToolChoice(uniai.ToolChoiceAuto()),
	)
	if err != nil {
		log.Fatal(err)
	}

	// No tool call means final answer.
	if len(resp.ToolCalls) == 0 {
		log.Println(resp.Text)
		break
	}

	// IMPORTANT: append assistant tool calls exactly as returned.
	messages = append(messages, uniai.Message{
		Role:      uniai.RoleAssistant,
		Content:   resp.Text,
		ToolCalls: resp.ToolCalls,
	})

	for _, tc := range resp.ToolCalls {
		var in struct {
			Text string `json:"text"`
		}
		result := map[string]any{"error": "invalid arguments"}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &in); err == nil {
			result = map[string]any{"text": in.Text}
		}
		b, _ := json.Marshal(result)

		// IMPORTANT: use tc.ID as-is when sending tool results.
		messages = append(messages, uniai.ToolResult(tc.ID, string(b)))
	}
}
```

Notes:
- Do not rebuild tool calls manually (for example, `id: "call_1"`). Rebuilding can lose provider-specific metadata.
- For Gemini native tool calling, missing tool-call metadata in follow-up rounds will cause request errors.

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

Check out the [stream demo](cmd/stream/README.md) for a runnable terminal example.

Supported providers: OpenAI-compatible (`openai`, `openai_custom`, `deepseek`, `xai`, `groq`), Azure, Anthropic, Bedrock. Susanoo and Cloudflare ignore streaming and fall back to blocking.

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

- OpenAI/OpenAI-compatible: `OpenAIAPIKey`, `OpenAIAPIBase`, `OpenAIModel`
- Azure OpenAI: `AzureOpenAIAPIKey`, `AzureOpenAIEndpoint`, `AzureOpenAIModel`
- Anthropic: `AnthropicAPIKey`, `AnthropicModel`
- AWS Bedrock: `AwsKey`, `AwsSecret`, `AwsRegion`, `AwsBedrockModelArn`
- Susanoo: `SusanooAPIBase`, `SusanooAPIKey`
- Cloudflare Workers AI: `CloudflareAccountID`, `CloudflareAPIToken`, `CloudflareAPIBase`
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
