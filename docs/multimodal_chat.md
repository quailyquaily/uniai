# Multimodal Chat (V1)

This document describes the currently implemented multimodal chat behavior in `uniai`.

## Scope

- API: `Chat`
- Direction: input-focused multimodal support (text + image)
- Backward compatibility: existing `Message.Content` and `Result.Text` flows continue to work

## Data Model

`chat.Message` supports both legacy text content and structured parts:

- `Content string` (legacy)
- `Parts []Part` (structured)

`Part` types currently supported:

- `text`
- `image_url`
- `image_base64`

Helper constructors:

- `uniai.UserParts(...)`
- `uniai.SystemParts(...)`
- `uniai.AssistantParts(...)`
- `uniai.TextPart(...)`
- `uniai.ImageURLPart(...)`
- `uniai.ImageBase64Part(...)`

## Validation Rules

### Part type validation

- `text`: valid text part
- `image_url`: requires non-empty `url`
- `image_base64`: requires non-empty `data_base64`
- any other `type`: rejected

### Role constraints

- `user`: can include `text`, `image_url`, `image_base64`
- `system`: text-only
- `assistant`: text-only
- `tool`: text-only

If a non-text part is used in non-user roles, request build fails with an explicit error.

## Normalization Rules

### Request normalization

For each message:

1. If `Parts` is non-empty, `Parts` is used.
2. Otherwise, if `Content` is non-empty, it is treated as a single `text` part.
3. Empty message content remains allowed only where already valid (for example, assistant tool-call messages).

### Response normalization

- `Result.Text` remains the compatibility output field.
- `Result.Parts` is populated with structured output when available.
- `Client.Chat()` also ensures `Result.Parts` contains at least one text part when `Result.Text` is non-empty.

## Provider Support Matrix (Current)

- OpenAI-compatible (`openai`, `openai_custom`, `deepseek`, `xai`, `groq`): supports `user` `text`, `image_url`, `image_base64`.
- Azure (`azure`): same mapping path as OpenAI-compatible.
- Gemini (`gemini`):
  - supports `user` `text` and `image_base64`
  - rejects `user` `image_url` with explicit unsupported error
- Anthropic (`anthropic`): text-only in chat path
- Bedrock (`bedrock`): text-only in chat path
- Cloudflare (`cloudflare`): text-only in chat path

## Usage Examples

### OpenAI-compatible: URL image + text

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
    return err
}
fmt.Println(resp.Text)
```

### OpenAI-compatible: base64 image + text

```go
resp, err := client.Chat(ctx,
    uniai.WithProvider("openai"),
    uniai.WithModel("gpt-5.2"),
    uniai.WithMessages(
        uniai.UserParts(
            uniai.TextPart("What is in this image?"),
            uniai.ImageBase64Part("image/jpeg", base64JPEG),
        ),
    ),
)
```

Notes:

- For `image_base64`, if MIME type is empty, providers that support this path default it to `image/png`.
- OpenAI-compatible mapping sends base64 image as a `data:<mime>;base64,<data>` URL.

### Gemini: base64 image + text

```go
resp, err := client.Chat(ctx,
    uniai.WithProvider("gemini"),
    uniai.WithModel("gemini-2.5-pro"),
    uniai.WithMessages(
        uniai.UserParts(
            uniai.TextPart("Describe this image."),
            uniai.ImageBase64Part("image/png", base64PNG),
        ),
    ),
)
```

## Common Errors

- `unsupported part type "audio_base64"`: unsupported `Part.Type`
- `role "assistant" supports only "text" part type`: non-text part used in non-user role
- `gemini provider model "...": role "user": unsupported part type "image_url"`: Gemini currently does not accept `image_url` in this path

## Related

- Historical design proposal: [`docs/feat_20260212_multimodal_chat_api.md`](feat_20260212_multimodal_chat_api.md)
- Tool calling behavior: [`docs/tool_emulation.md`](tool_emulation.md)
