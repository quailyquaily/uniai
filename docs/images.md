# Image Generation

This document describes the currently implemented image generation behavior in `uniai`.

## Scope

- API: `Image`
- Direction: text-to-image generation
- Not included: image editing, image variations, and Responses API multi-turn image generation

## Quick Start

```go
img, err := client.Image(ctx,
    uniai.Image("gpt-image-2", "a minimal line-art cat"),
    uniai.WithCount(1),
)
```

`ImageResult.Data[*].B64JSON` contains base64-encoded image data. `ImageResult.MimeType` contains the normalized MIME type when the provider reports or implies one.

## Provider Routing

If `WithImageProvider(...)` is not set, `uniai` chooses a provider from the model name:

- `gemini-*` or `imagen-*`: Gemini
- any model containing `gpt-`: OpenAI
- `@cf/*`: Cloudflare Workers AI
- everything else: OpenAI

## Supported Models

OpenAI uses the Images API (`/v1/images/generations`). The OpenAI path supports GPT image generation models such as:

- `gpt-image-2`
- `gpt-image-2-*` snapshot IDs
- `gpt-image-1.5`
- `gpt-image-1`
- `gpt-image-1-mini`

Gemini has an explicit local allowlist:

- `imagen-3.0-generate-002`
- `gemini-2.5-flash-image`
- `gemini-3-pro-image-preview`

Cloudflare Workers AI accepts `@cf/...` model IDs and forwards provider-specific options to Workers AI. Models containing `flux-2-` use multipart requests automatically.

## OpenAI `gpt-image-2`

Pass OpenAI-specific parameters through `WithImageOptions`:

```go
img, err := client.Image(ctx,
    uniai.Image("gpt-image-2", "a poster-style illustration of a quiet reading room"),
    uniai.WithCount(1),
    uniai.WithImageOptions(uniai.ImageOptions{
        OpenAI: structs.JSONMap{
            "size":               "2048x1152",
            "quality":            "auto",
            "output_format":      "jpeg",
            "output_compression": 80,
            "background":         "auto",
            "moderation":         "auto",
        },
    }),
)
```

Supported `gpt-image-2` options:

- `size`: `auto` or `<width>x<height>`.
- `quality`: `low`, `medium`, `high`, or `auto`.
- `output_format`: `png`, `jpeg`, or `webp`. `jpg` is accepted and sent as `jpeg`.
- `output_compression`: integer from 0 to 100. Valid only with `jpeg` or `webp`.
- `background`: `auto` or `opaque`. `transparent` is rejected for `gpt-image-2`.
- `moderation`: `auto` or `low`.
- `user`: optional end-user identifier.

For `gpt-image-2`, custom `size` values must satisfy OpenAI's constraints:

- both edges are multiples of 16
- maximum edge length is 3840 px
- long edge to short edge ratio is at most 3:1
- total pixels are between 655,360 and 8,294,400

## Gemini Options

Gemini options are passed through `WithImageOptions(uniai.ImageOptions{Gemini: ...})`.

For `imagen-3.0-generate-002`:

- `aspect_ratio` or `aspectRatio`
- `safety_filter_level`
- `person_generation`

For `gemini-2.5-flash-image` and `gemini-3-pro-image-preview`:

- `aspect_ratio` or `aspectRatio`
- `response_modalities` or `responseModalities`
- `image_size` or `imageSize`

## Cloudflare Options

Cloudflare options are passed through `WithImageOptions(uniai.ImageOptions{Cloudflare: ...})` and merged into the provider payload.

- `prompt` is set from `uniai.Image(...)` unless already provided.
- `num_images` is set from `WithCount(...)` unless already provided.
- `multipart: true` forces multipart requests.
- Models containing `flux-2-` use multipart requests automatically.
