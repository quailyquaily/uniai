# stream CLI

`cmd/stream` is a minimal streaming demo for `uniai.Chat`.
It supports `openai` provider only and prints model output to terminal in real time.

## Run

```bash
source cmd/stream/env.example.sh
# fill OPENAI_API_KEY and OPENAI_MODEL (or pass --model)

go run ./cmd/stream
```

Optional flags:

```bash
go run ./cmd/stream \
  --model gpt-5.2
```

## Environment Variables

- `OPENAI_API_KEY` (required)
- `OPENAI_MODEL` (required unless `--model` is set)
- `OPENAI_API_BASE` (optional)

Defaults in code:

- prompt: built-in long-form prompt for large streaming output
- timeout: `180s`
- max tokens: `4096`
