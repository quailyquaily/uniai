# Image Edit API Design (2026-05-13)

## Status

- Implemented on the `feat/image-2` branch
- Scope: `image` package, root re-exports, and provider adapters
- First upstream targets:
  - OpenAI Images API: `gpt-image-2`, `gpt-image-1`
  - Google Gemini native image generation:
    - Nano Banana Pro: `gemini-3-pro-image-preview`
    - Nano Banana 2: `gemini-3.1-flash-image-preview`
- References checked on 2026-05-13:
  - OpenAI image generation guide
  - OpenAI Images API reference
  - Google Gemini image generation guide
- Maintainer-confirmed model mapping:
  - Nano Banana 2: `gemini-3.1-flash-image-preview`

## Goal

Add image editing to `uniai` without binding the public API to one upstream's transport or state model.

Callers should be able to:

- generate an image from text
- edit one or more input images with a prompt
- read generated images in a provider-neutral result shape
- inspect token usage and local cost estimates when upstream usage exists

## Problem

The current `image.Result` mirrors OpenAI's generation response:

```go
Data []struct {
    B64JSON string `json:"b64_json"`
} `json:"data"`
MimeType string `json:"mime_type"`
```

That is too narrow for editing:

- OpenAI Images returns base64 output but accepts multipart file inputs.
- Gemini returns inline image data and can also emit text.
- Some providers return URLs instead of inline base64.
- Per-image metadata belongs on each image, not on the whole result.

Using only `Data[*].B64JSON` makes every provider look like OpenAI. That is simple for the first version of generation, but it is not a good base for image editing.

## Design Principles

1. Keep the public API small.
2. Use bytes as the caller-facing image input format.
3. Let each provider choose the best upstream transport: multipart, inline base64, or JSON.
4. Do not expose local file paths in the public API. Callers can read files themselves.
5. Keep generation and editing stateless in the first pass.
6. Preserve backward compatibility for existing `Result.Data[*].B64JSON` callers.
7. Do not build an OpenAI Responses session abstraction inside the generic `image` package.
8. Return raw provider responses for debugging, but keep common fields easy to use.

## Non-Goals

- No first-pass image variations API.
- No first-pass OpenAI Responses multi-turn session API.
- No first-pass file upload lifecycle API.
- No first-pass provider-neutral image storage abstraction.
- No first-pass mask support.
- No attempt to hide provider limits such as size or count constraints.
- No streaming partial images in the first edit API.

## Public API Proposal

### Image input

Use bytes, not base64 strings, as the main input type:

```go
type InputImage struct {
    Filename string `json:"filename,omitempty"`
    MIMEType string `json:"mime_type,omitempty"`
    Data     []byte `json:"-"`
}
```

Reasons:

- It is easy for callers to build from files, HTTP downloads, or previous image results.
- OpenAI Images can send it as multipart without base64 expansion.
- Gemini can encode it as inline base64 inside the provider adapter.
- The public API does not leak local paths.
- `Filename` should be a display filename, not a local path. Providers may use it only as a multipart filename.

### Image result

Add a provider-neutral image asset list:

```go
type Result struct {
    Created int                `json:"created"`
    Images  []ImageAsset       `json:"images,omitempty"`
    Text    string             `json:"text,omitempty"`
    Usage   CreateImageUsage   `json:"usage"`
    Raw     json.RawMessage    `json:"-"`

    // Deprecated compatibility fields.
    Data     []ImageData `json:"data,omitempty"`
    MimeType string      `json:"mime_type,omitempty"`
}

type ImageAsset struct {
    DataBase64    string `json:"data_base64,omitempty"`
    URL           string `json:"url,omitempty"`
    MIMEType      string `json:"mime_type,omitempty"`
    RevisedPrompt string `json:"revised_prompt,omitempty"`
}

type ImageData struct {
    B64JSON string `json:"b64_json"`
}
```

Mapping rules:

- Always fill `Images` when image output exists.
- Store inline image output on `Images[*].DataBase64`, not an OpenAI-specific field name.
- Keep filling `Data` from `Images[*].DataBase64` when base64 output exists.
- Keep `MimeType` as a compatibility shortcut only when every returned image has the same MIME type.
- Store per-image MIME type on `ImageAsset.MIMEType`.
- Store URL outputs on `ImageAsset.URL` without downloading them.
- Store provider text output on `Result.Text` when an image model returns both text and images.
- `Raw` contains the upstream response bytes as `json.RawMessage` for debugging. It is not a stable contract and should not be used for normal control flow.

### Generate API

Keep the existing generation entrypoint:

```go
img, err := client.Image(ctx,
    uniai.Image("gpt-image-2", "a minimal line-art cat"),
    uniai.WithImageOptions(uniai.ImageOptions{
        OpenAI: structs.JSONMap{
            "size": "1024x1024",
        },
    }),
)
```

After the result change, generation should fill both:

- `Result.Images`
- existing `Result.Data` compatibility fields

### Edit API

Add a separate edit request and entrypoint:

```go
img, err := client.EditImage(ctx,
    uniai.ImageEdit(
        "gpt-image-2",
        "make the background plain white",
        uniai.InputImage{
            Filename: "product.png",
            MIMEType: "image/png",
            Data:     productPNG,
        },
    ),
    uniai.WithImageEditOptions(uniai.ImageOptions{
        OpenAI: structs.JSONMap{
            "size":          "1024x1024",
            "quality":       "high",
            "output_format": "webp",
        },
    }),
)
```

Proposed types:

```go
type EditRequest struct {
    Provider string       `json:"provider,omitempty"`
    Model    string       `json:"model,omitempty"`
    Prompt   string       `json:"prompt,omitempty"`
    Images   []InputImage `json:"-"`
    Count    int          `json:"count,omitempty"`
    Options  Options      `json:"options,omitempty"`
}

type ImageEditOption func(*EditRequest)
```

Helpers:

```go
func ImageEdit(model, prompt string, images ...InputImage) ImageEditOption
func WithImageEditProvider(provider string) ImageEditOption
func WithImageEditCount(count int) ImageEditOption
func WithImageEditOptions(opts Options) ImageEditOption
```

`WithImageEditOptions` has the same semantic purpose as `WithImageOptions`: pass provider-specific image options. It is a separate function because it applies to `EditRequest`, not `Request`, and edit-specific validation can differ.

The helper names should be exported from root `uniai` in the same style as current `Image(...)` helpers. The root helper should accept `uniai.ImageOptions`.

Without a mask, all input images are prompt-addressable reference images. Image order matters; callers should write prompts that make each image's role explicit, for example "use the first image as the style reference and redraw the second image."

## Model Names And Aliases

Use upstream model IDs as the stable API contract. Product names are useful for callers, but provider adapters should resolve them before sending requests.

First-pass aliases:

- `nano-banana-pro` -> `gemini-3-pro-image-preview`
- `nano-banana-2` -> `gemini-3.1-flash-image-preview`

Rules:

- aliases are local `uniai` routing conveniences
- alias normalization must happen before provider routing
- provider requests should use canonical upstream model IDs
- docs should show canonical model IDs first, aliases second

## Provider Mapping

### OpenAI

Use the Images API, not Responses API, for first-pass editing.

Generation:

- `POST /v1/images/generations`
- JSON body

Editing:

- `POST /v1/images/edits`
- multipart body
- fields:
  - `model`
  - `prompt`
  - repeated `image[]`
  - optional provider fields from `Options.OpenAI`

Do not send `mask` in the first pass. If a caller needs localized editing, they should express the target area in the prompt, or wait for a later provider-specific mask feature.

Supported first-pass models:

- `gpt-image-2`
- `gpt-image-2-*`
- `gpt-image-1`
- `gpt-image-1-*`

The OpenAI adapter should parse usage the same way for generation and edit responses:

- `usage.input_tokens`
- `usage.input_tokens_details.text_tokens`
- `usage.input_tokens_details.image_tokens`
- `usage.input_tokens_details.cached_text_tokens`
- `usage.input_tokens_details.cached_image_tokens`
- `usage.output_tokens`
- `usage.total_tokens`

### Google Gemini

Use Gemini native image generation via `generateContent`.

Supported first-pass models:

- Nano Banana Pro: `gemini-3-pro-image-preview`
- Nano Banana 2: `gemini-3.1-flash-image-preview`

Generation:

- send prompt as text content

Editing:

- send prompt plus one or more input image parts
- encode `InputImage.Data` as inline image data
- preserve MIME type when provided

If Gemini does not support multiple returned images for this path, reject `Count > 1` instead of silently ignoring it.

### Cloudflare

Cloudflare is not a first-pass edit target.

Keep existing generation support as-is. Add edit only when a specific Workers AI image edit model and request shape are chosen.

## Interactive Editing

Keep first-pass editing stateless.

Callers can implement iterative edits by feeding the previous result back as input:

```go
first, err := client.EditImage(ctx, ...)
if err != nil {
    return err
}

nextInput, err := first.Images[0].AsInputImage()
if err != nil {
    return err
}

second, err := client.EditImage(ctx,
    uniai.ImageEdit("gpt-image-2", "make it more realistic", nextInput),
)
```

Add a small helper for base64 outputs:

```go
func (a ImageAsset) AsInputImage() (InputImage, error)
```

This helper should:

- decode `DataBase64`
- carry `MIMEType`
- infer MIME type with `http.DetectContentType` when `MIMEType` is empty
- set a simple display filename such as `image.png` when one can be inferred
- return an error for URL-only assets

Do not add a URL downloader helper in the first pass. Callers should own network fetches so they can control authentication, retries, size limits, and storage policy.

Do not add session state to `image.Result`. OpenAI Responses `previous_response_id` can be supported later by an OpenAI-specific API if needed.

## Validation

Shared validation:

- `model` is required
- `prompt` is required
- edit requires at least one input image
- each input image requires non-empty `Data`
- MIME type should be recommended, but only required when the provider needs it

Provider validation:

- OpenAI validates supported size, quality, background, format, compression, and model-specific constraints.
- Gemini validates supported model IDs and option keys already accepted by the provider.

## Pricing And Usage

Reuse current image pricing support.

Editing uses the same `CreateImageUsage` fields:

```go
InputTokens
InputTextTokens
InputImageTokens
CachedTextTokens
CachedImageTokens
OutputTokens
TotalTokens
Cost
```

Cost formulas do not need a separate edit path:

- text input cost uses `InputTextTokens`
- image input cost uses `InputImageTokens`
- cached input discounts use the cached fields
- image output cost uses `OutputTokens`

If a provider does not return usage, leave usage fields zero and do not estimate cost.

## Compatibility

Existing callers using `Result.Data[*].B64JSON` must continue to work.

Migration path:

1. Add `Result.Images`.
2. Fill both `Images` and `Data`.
3. Document `Data` and `MimeType` as deprecated compatibility fields.
4. Keep `Data` for one minor release series.

## Implementation Checklist

- [x] Add `InputImage`, `ImageAsset`, and `ImageData`.
- [x] Add `Result.Images`, `Result.Text`, and `Result.Raw`; keep `Data` and `MimeType`.
- [x] Add `EditRequest`, `ImageEditOption`, and edit helper constructors.
- [x] Add `Client.EditImage`.
- [x] Add provider routing for edit requests.
- [x] Add local model aliases for `nano-banana-pro` and `nano-banana-2`.
- [x] Normalize image model aliases before provider routing.
- [x] Implement OpenAI multipart `/images/edits`.
- [x] Map OpenAI edit response usage into `CreateImageUsage`.
- [x] Implement Gemini edit via `generateContent` with inline image parts.
- [x] Keep mask unsupported in the first pass.
- [x] Add unit tests for OpenAI multipart request construction.
- [x] Add unit tests for Gemini edit request construction.
- [x] Add result normalization tests for base64, URL, MIME type, and compatibility `Data`.
- [x] Update `docs/images.md`.
- [x] Update README image section only if the public API changes.
