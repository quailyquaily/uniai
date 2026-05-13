# Image Generation and Editing

This document describes the currently implemented image behavior in `uniai`.

## Scope

- APIs: `Image`, `EditImage`
- Directions: text-to-image generation and prompt-based image editing
- Not included: image variations, mask-based localized editing, and Responses API multi-turn image generation

## Quick Start

Generate an image:

```go
img, err := client.Image(ctx,
    uniai.Image("gpt-image-2", "a minimal line-art cat"),
    uniai.WithCount(1),
)
```

Edit from one or more input images:

```go
edited, err := client.EditImage(ctx,
    uniai.ImageEdit("gpt-image-2", "redraw this in a clean product-photo style",
        uniai.InputImage{
            Filename: "source.png",
            MIMEType: "image/png",
            Data:     sourcePNG,
        },
    ),
    uniai.WithImageEditOptions(uniai.ImageOptions{
        OpenAI: structs.JSONMap{
            "size":          "1024x1024",
            "quality":       "medium",
            "output_format": "png",
        },
    }),
)
```

Prefer `ImageResult.Images[*]` for generated output. Inline image bytes are stored as base64 on `ImageResult.Images[*].DataBase64`. `ImageResult.Data[*].B64JSON` and `ImageResult.MimeType` remain as compatibility fields for one minor release series.

`ImageResult.Raw` contains the upstream image API response as `json.RawMessage` for debugging.

When the provider returns image token usage and the active pricing catalog has a matching `image` rule, `ImageResult.Usage.Cost` contains a local USD estimate. See [`docs/pricing.md`](pricing.md) for catalog fields and formulas.

## Provider Routing

If `WithImageProvider(...)` is not set, `uniai` chooses a provider from the model name:

- `gemini-*` or `imagen-*`: Gemini
- any model containing `gpt-`: OpenAI
- `@cf/*`: Cloudflare Workers AI
- everything else: OpenAI

## Supported Models

OpenAI uses the Images API:

- `Image`: `POST /v1/images/generations`
- `EditImage`: `POST /v1/images/edits`

The OpenAI path supports GPT image models such as:

- `gpt-image-2`
- `gpt-image-2-*` snapshot IDs
- `gpt-image-1.5`
- `gpt-image-1`
- `gpt-image-1-mini`

Gemini has an explicit local allowlist:

- `imagen-3.0-generate-002`
- `gemini-2.5-flash-image`
- `gemini-3-pro-image-preview` (`nano-banana-pro`)
- `gemini-3.1-flash-image-preview` (`nano-banana-2`)

Cloudflare Workers AI accepts `@cf/...` model IDs and forwards provider-specific options to Workers AI. Models containing `flux-2-` use multipart requests automatically.

`EditImage` is currently implemented for:

- OpenAI `gpt-image-2`, `gpt-image-2-*`, `gpt-image-1`, and `gpt-image-1-*`
- Gemini `gemini-3-pro-image-preview` and `gemini-3.1-flash-image-preview`

Cloudflare image editing is not implemented yet.

## OpenAI `gpt-image-2`

Pass OpenAI-specific generation parameters through `WithImageOptions`:

```go
img, err := client.Image(ctx,
    uniai.Image("gpt-image-2", "a poster-style illustration of a quiet reading room"),
    uniai.WithCount(1),
    uniai.WithImageOptions(uniai.ImageOptions{
        OpenAI: structs.JSONMap{
            "size":               "2048x1152",
            "quality":            "medium",
            "output_format":      "jpeg",
            "output_compression": 80,
            "background":         "auto",
            "moderation":         "auto",
        },
    }),
)
```

For edits, use the same provider-specific fields through `WithImageEditOptions`.

Supported OpenAI `gpt-image-2` options:

- `size`: `auto` or `<width>x<height>`.
- `quality`: `low`, `medium`, `high`, or `auto`. If omitted, `uniai` sends `medium`.
- `output_format`: `png`, `jpeg`, or `webp`. `jpg` is accepted and sent as `jpeg`. If omitted, `uniai` sends `webp`.
- `output_compression`: integer from 0 to 100. Valid only with `jpeg` or `webp`.
- `background`: `auto` or `opaque`. `transparent` is rejected for `gpt-image-2`.
- `moderation`: `auto` or `low`.
- `user`: optional end-user identifier.

For `gpt-image-2`, custom `size` values must satisfy OpenAI's constraints:

- both edges are multiples of 16
- maximum edge length is 3840 px
- long edge to short edge ratio is at most 3:1
- total pixels are between 655,360 and 8,294,400

`EditImage` does not send `mask` in the first pass. Without a mask, all input images are prompt-addressable reference images. Image order matters; write prompts that identify each image's role, such as "use the first image as the style reference and redraw the second image."

## Gemini Options

Gemini options are passed through `WithImageOptions(uniai.ImageOptions{Gemini: ...})`.

For `imagen-3.0-generate-002`:

- `aspect_ratio` or `aspectRatio`
- `safety_filter_level`
- `person_generation`

For `gemini-2.5-flash-image`, `gemini-3-pro-image-preview`, and `gemini-3.1-flash-image-preview`:

- `aspect_ratio` or `aspectRatio`
- `response_modalities` or `responseModalities`
- `image_size` or `imageSize`

Gemini image editing sends input images as inline image parts to `generateContent`. `EditImage` currently returns one image per request for Gemini; `WithImageEditCount` greater than 1 is rejected on that path.

## Cloudflare Options

Cloudflare options are passed through `WithImageOptions(uniai.ImageOptions{Cloudflare: ...})` and merged into the provider payload.

- `prompt` is set from `uniai.Image(...)` unless already provided.
- `num_images` is set from `WithCount(...)` unless already provided.
- `multipart: true` forces multipart requests.
- Models containing `flux-2-` use multipart requests automatically.
