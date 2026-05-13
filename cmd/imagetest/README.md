# cmd/imagetest

`cmd/imagetest` is a live test command for `uniai.Image` and `uniai.EditImage`.

It reads provider credentials from:

- `OPENAI_API_KEY`
- `GEMINI_API_KEY`

Run image generation with every configured provider:

```bash
go run ./cmd/imagetest --mode generate
```

Run OpenAI generation and editing:

```bash
OPENAI_API_KEY=... go run ./cmd/imagetest --provider openai --mode all
```

Run Gemini generation and editing:

```bash
GEMINI_API_KEY=... go run ./cmd/imagetest --provider gemini --mode all
```

Editing reads these images by default:

- `cmd/imagetest/sample-1.jpg`: first target image
- `cmd/imagetest/sample-2.jpg`: second target image
- `cmd/imagetest/sample-3.png`: style reference image

The default edit prompt asks the model to understand the style of `sample-3` and redraw `sample-1` and `sample-2` in that style. Override image paths with `--sample1`, `--sample2`, and `--sample3`.

Generated files are written to `cmd/imagetest/out` by default. Override with `--out`.
