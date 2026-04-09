# cachetest

`cmd/cachetest` is a live validation CLI for prompt caching behavior.

It exercises real providers using environment-provided credentials and checks:

- `stats`: repeated identical requests surface cache-hit token counts
- `scope`: explicit cache boundaries preserve hits for the reusable prefix and reduce hits when content before the boundary changes
- `stream`: final streaming usage includes cache-hit token counts

## Run

```bash
source cmd/cachetest/env.example.sh
# fill in real credentials in your shell first

go run ./cmd/cachetest --provider openai_resp --scene stats
go run ./cmd/cachetest --provider anthropic --scene scope --ttl 5m
go run ./cmd/cachetest --provider anthropic --scene stream
```

Useful flags:

- `--provider`
- `--scene`
- `--model`
- `--ttl`
- `--timeout`
- `--stream`

`--stream` is a shortcut for `--scene stream`.

## Environment Variables

Common:

- `PROVIDER`
- `MODEL`
- `CACHE_SCENE`
- `CACHE_TTL`
- `CACHE_STREAM`
- `TIMEOUT_SECONDS`
- `PROMPT_CACHE_RETENTION` or `CACHE_RETENTION` for OpenAI-family request-level retention

OpenAI / OpenAI Responses:

- `OPENAI_API_KEY`
- `OPENAI_API_BASE` (optional)
- `OPENAI_MODEL` (optional fallback when `MODEL` is empty)

Azure:

- `AZURE_OPENAI_API_KEY`
- `AZURE_OPENAI_ENDPOINT`
- `AZURE_OPENAI_DEPLOYMENT` or `AZURE_OPENAI_MODEL`
- `AZURE_OPENAI_API_VERSION` (optional, default `2024-08-01-preview`)

Anthropic:

- `ANTHROPIC_API_KEY`
- `ANTHROPIC_MODEL` (optional fallback when `MODEL` is empty)

Bedrock:

- `AWS_ACCESS_KEY_ID`
- `AWS_SECRET_ACCESS_KEY`
- `AWS_REGION` (optional, default `us-east-1`)
- `BEDROCK_MODEL_ARN` (optional fallback when `MODEL` is empty)

## Output

The CLI prints:

- a short human-readable summary
- an indented JSON summary to stdout

It exits non-zero when the expected cache behavior is not observed.
