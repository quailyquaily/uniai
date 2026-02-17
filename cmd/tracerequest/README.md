# tracerequest CLI

`cmd/tracerequest` is a small demo CLI for `WithDebugFn`.
It captures request/response debug payloads and writes them to `./dump`.

## Run

```bash
source cmd/tracerequest/env.example.sh
# fill real values in your shell, e.g. OPENAI_API_KEY / MODEL

go run ./cmd/tracerequest run
```

Optional flags:

```bash
go run ./cmd/tracerequest \
  --provider openai \
  --scene toolcalling \
  --model gpt-5.2 \
  --prompt "Say hello" \
  --timeout 90 \
  --dump-dir dump \
```

`--timeout` 和 `--dump-dir` 可省略，默认分别是 `90` 和 `dump`。
`run` 子命令可写可不写。

## Output

A dump file is created under `./dump`:

- filename: `YYYY-MM-DD_hh-mm-ss.md`
- format:

```text
## "{provider}.{function}.{request|response}"
* time: YYYY-MM-DD hh:mm:ss
* payload: |
{payload}
```

## Environment Variables

Common:

- `PROVIDER` (default: `openai`; supported: `openai`, `cloudflare`, `gemini`)
- `MODEL` (required for most providers; can be overridden by `--model`)
- `SCENE` (`none` or `toolcalling`, default: `none`)
- `PROMPT` (default: `Reply with exactly: tracerequest-demo`)
- `TIMEOUT_SECONDS` (default: `90`)
- `DUMP_DIR` (default: `dump`)

When `SCENE=toolcalling` (or `--scene toolcalling`), tracerequest injects the same mock tool set used in `cmd/speedtest` (`get_weather`, `get_direction`, `send_message`) and performs multi-turn loop:

1. send user+tools (`tool_choice=required` for the first 2 rounds)
2. if model returns `tool_calls`, append assistant `tool_calls` as-is and append mock tool results
3. continue next round until no more `tool_calls` (or max round reached)

This is designed to cover provider flows that require preserving fields like `thought_signature` across tool-calling turns.

OpenAI:

- `OPENAI_API_KEY`
- `OPENAI_API_BASE` (optional)
- `OPENAI_MODEL` (optional fallback when `MODEL` is empty)

Cloudflare:

- `CLOUDFLARE_ACCOUNT_ID`
- `CLOUDFLARE_API_TOKEN`
- `CLOUDFLARE_MODEL` (optional fallback when `MODEL` is empty)
- `CLOUDFLARE_API_BASE` (optional)

Gemini:

- `GEMINI_API_KEY`
- `GEMINI_MODEL` (optional fallback when `MODEL` is empty)
- `GEMINI_API_BASE` (optional)
