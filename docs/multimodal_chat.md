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

- OpenAI-compatible (`openai`, `deepseek`, `xai`, `groq`): supports `user` `text`, `image_url`, `image_base64`.
- Azure (`azure`): same mapping path as OpenAI-compatible.
- Gemini (`gemini`):
  - supports `user` `text` and `image_base64`
  - rejects `user` `image_url` with explicit unsupported error
- Anthropic (`anthropic`): supports `user` `text`, `image_url`, `image_base64`
- Bedrock (`bedrock`): text-only in chat path
- Cloudflare (`cloudflare`): text-only in chat path

## Mainstream Model Image-Input Support (as of 2026-03-01)

The table below combines model-level capability reference and current `uniai` support status.

| Model / Ecosystem | Model capability (official docs) | `uniai` status | `uniai` provider path | Image input in `uniai` |
| --- | --- | --- | --- | --- |
| OpenAI (GPT-5/5.1, GPT-4.1, GPT-4o, o3) | Supports image input | Supported | `openai`, `azure` | `image_url`, `image_base64` |
| Google Gemini (3.1/2.5 family) | Supports image input | Partially supported | `gemini` | `image_base64` only (`image_url` rejected) |
| xAI Grok | Some models support image input | Supported (OpenAI-compatible path) | `xai` | `image_url`, `image_base64` |
| Anthropic Claude | Current models support image input | Supported | `anthropic` | `image_url`, `image_base64` |
| Mistral Vision models | Vision-capable models available | Conditionally supported | `openai` + `OpenAIAPIBase` (when backend is OpenAI-compatible) | Backend-dependent (typically `image_url` / `image_base64`) |
| Qwen2.5-VL | Supports image input | Conditionally supported | `openai` + `OpenAIAPIBase` (when backend is OpenAI-compatible) | Backend-dependent |
| Llama 3.2 Vision | Supports image input | Conditionally supported | `openai` + `OpenAIAPIBase` (when backend is OpenAI-compatible) | Backend-dependent |

References:

- OpenAI GPT-5: <https://developers.openai.com/api/docs/models/gpt-5>
- OpenAI GPT-4.1: <https://developers.openai.com/api/docs/models/gpt-4.1>
- OpenAI o3: <https://developers.openai.com/api/docs/models/o3>
- Anthropic model overview: <https://platform.claude.com/docs/en/about-claude/models/overview>
- Anthropic vision docs: <https://platform.claude.com/docs/en/build-with-claude/vision>
- Gemini 3.1 Pro Preview: <https://ai.google.dev/gemini-api/docs/models/gemini-3.1-pro-preview>
- Gemini 2.5 Pro: <https://ai.google.dev/gemini-api/docs/models/gemini-2.5-pro>
- Gemini 2.5 Flash: <https://ai.google.dev/gemini-api/docs/models/gemini-2.5-flash>
- xAI image understanding: <https://docs.x.ai/developers/model-capabilities/images/understanding>
- xAI models: <https://docs.x.ai/developers/models>
- Mistral vision: <https://docs.mistral.ai/capabilities/vision>
- Qwen2.5-VL-72B-Instruct: <https://huggingface.co/Qwen/Qwen2.5-VL-72B-Instruct>
- Llama-3.2-11B-Vision-Instruct: <https://huggingface.co/meta-llama/Llama-3.2-11B-Vision-Instruct>

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
