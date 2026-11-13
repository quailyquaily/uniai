# modelcheck

`cmd/modelcheck` runs the same five live chat checks against many models and
prints a pass/fail table:

| Case | Request | Passes when |
| --- | --- | --- |
| `text` | "Reply with exactly: modelcheck-ok" | the reply contains `modelcheck-ok` |
| `json` | describe Tokyo as a JSON object | the reply parses and has `city`, `country`, and numeric `population_millions` |
| `cache` | the same ~9k-token prompt twice | the repeat reports `Usage.Cache.CachedInputTokens > 0`; the table shows the hit rate (cached ÷ input tokens), e.g. `PASS 99.6%` |
| `tool` | "What's the weather in Tokyo?" with a `get_weather(city, unit)` tool | the model calls `get_weather` with a city containing Tokyo |
| `pricing` | no extra request; reuses the responses above (one small request if run alone) | uniai's pricing catalog has a rate for the model, and every response in the run got a cost estimate |

These are live API calls and cost money. The cache case alone sends at least
two ~9k-token requests per model. Use `--list` to check credentials first.

## Run

```bash
export OPENAI_API_KEY=... CLAUDE_API_KEY=... GEMINI_API_KEY=...
export DEEPSEEK_API_KEY=... MOONSHOT_API_KEY=...

go run ./cmd/modelcheck --list                     # no API calls
go run ./cmd/modelcheck --config cmd/modelcheck/my.local.yaml   # your own config
go run ./cmd/modelcheck                            # all models, all cases
go run ./cmd/modelcheck --only gemini-4,claude     # name or model substrings
go run ./cmd/modelcheck --cases text,tool          # subset of cases
go run ./cmd/modelcheck --json ./out/modelcheck.json -v
```

Without `--config`, the built-in [config.example.yaml](config.example.yaml)
is used. It lists current GPT-6 / GPT-6.1, Claude 5.5, Gemini 4 and 3.8 Flash,
Kimi K3 / K3.1 Pro, `deepseek-flash`, and `deepseek-v4-pro`. Models whose key
variable is empty are skipped, not failed.
For your own models or gateways, copy it to `cmd/modelcheck/<name>.local.yaml`.
That pattern is gitignored, so private gateway names stay out of the repo.

Flags:

- `--config`: YAML config path. Default: built-in config.
- `--only`: comma-separated names or substrings of the name or model.
- `--cases`: comma-separated subset of `text,json,cache,tool,pricing`.
  `pricing` always runs last.
- `--parallel`: models checked concurrently. Default `4`. Cases for one model
  run in order.
- `--json`: write the full report, including usage and responses.
- `--list`: show each model's key variable, API base, and catalog price
  (input, cached input, and output per 1M tokens), then exit.
- `-v`: print every case's detail and response, not only failures.
- `--color`: `auto` (default) colors output only on a terminal, and turns it
  off when `NO_COLOR` is set or `TERM=dumb`. `always` forces color, for
  example through `less -R`; `never` disables it.

Results are colored when supported: PASS green, FAIL red, SKIP yellow. The
run ends with a one-line total of passed, failed, and skipped cases.

The exit code is `1` when any case fails, `2` on a usage or config error, and
`0` otherwise. Skips do not fail the run.

## Config

Each entry under `providers` is one endpoint and the models to test on it.
Results are named `<provider name>/<model>`, so `--only openrouter` selects a
whole group.

```yaml
providers:
  - name: openai               # a route name: uses OPENAI_API_KEY
    provider: openai_resp      # route, when it differs from the name
    reasoning_effort: low      # default for every model below
    models:
      - gpt-6-luna
      - gpt-6.1-terra
      - model: gpt-6-sol       # per-model overrides
        reasoning_effort: high

  - name: openrouter           # any other name is an OpenAI-compatible gateway:
    models:                    # reads OPENROUTER_API_KEY and OPENROUTER_API_BASE
      - anthropic/claude-sonnet-5.5
      - openai/gpt-6-sol

  - name: abc                  # reads ABC_API_KEY and ABC_API_BASE
    models:
      - gpt-6-sol
      - model: gpt-6-sol       # same model twice needs a distinct name
        name: gpt-6-sol-prompt-json
        json_mode: prompt
        cases: [json]
```

Credentials never go in the config file. The env var names come from the
provider `name`:

- A route name (`openai`, `openai_resp`, `anthropic`, `gemini`, `deepseek`,
  `xai`, `groq`, `meta`, `sakana`) selects that route and its usual variables:
  `OPENAI_API_KEY`; `CLAUDE_API_KEY` then `ANTHROPIC_API_KEY`;
  `GEMINI_API_KEY`; `DEEPSEEK_API_KEY`; and so on. A base URL variable such as
  `OPENAI_API_BASE` is optional.
- Any other name is a gateway. It reads `<NAME>_API_KEY` and `<NAME>_API_BASE`,
  with non-alphanumeric characters turned into `_`: `open-router` reads
  `OPEN_ROUTER_API_KEY`. The base URL is required unless `api_base` is set.
  Gateways use the `openai` route unless `provider` says otherwise.

A model whose key or required base variable is empty is skipped.

### Provider fields

| Field | Default | Notes |
| --- | --- | --- |
| `name` | required | unique; also picks the default env vars |
| `provider` | the name if it is a route, else `openai` | uniai route: `openai`, `openai_resp`, `anthropic`, `gemini`, `deepseek`, `xai`, `groq`, `meta`, `sakana` |
| `api_key_env` | from `name` | overrides the key variable |
| `api_base_env` | from `name` | overrides the base variable; an empty value skips the group |
| `api_base` | none | literal URL; wins over `api_base_env` |
| `models` | required | model IDs, or mappings with `model`, optional `name`, and settings |

### Settings

Set these on a provider as defaults for its models, or on a model to override
them.

| Field | Default | Notes |
| --- | --- | --- |
| `reasoning_effort` | unset | sent as-is; `uniai` rejects values it knows a model does not support |
| `max_tokens` | top-level value | |
| `json_mode` | `auto` | `native` sends `response_format: json_object` (OpenAI-compatible) or `response_mime_type` (Gemini); `prompt` relies on the prompt only. `auto` is native except on `anthropic` |
| `cache_breakpoint` | `auto` | `auto` marks a 5m breakpoint on `anthropic`, and OpenAI's boundary on GPT-5.6/GPT-6 at api.openai.com; other routes use implicit caching. `on` / `off` force it |
| `cases` | all | subset of `text`, `json`, `cache`, `tool`, `pricing` |
| `timeout_seconds` | top-level value | |

Top level: `timeout_seconds` (per request, default `180`), `max_tokens`
(default unset), `cache_attempts` (repeats before the cache case fails, default
`3`), and `providers`.

Bedrock, Azure, and Cloudflare are not supported yet.

## Notes

- GPT-6 entries use `openai_resp`, because the `openai` provider rejects GPT-6
  tool calls with reasoning on api.openai.com.
- Every run uses a new prompt prefix, so the first cache request is always a
  cold write. On api.openai.com the cache case also sends a per-run
  `prompt_cache_key`.
- Implicit caches can lag. The cache case waits 2 seconds before each repeat.
- Some gateways do not report cached tokens. The cache case fails there even if
  caching works upstream.
- `COST` uses `uniai`'s pricing catalog and shows `-` for unpriced models.
- The `pricing` case catches two gaps: a new model with no catalog entry, and
  an API that reports a different model name than requested, such as a dated
  snapshot. Non-streaming costs use the reported name, so such responses get
  no cost. A cache hit on a model with no cached-input rate also gets no cost;
  the case reports that separately. It checks that a rate exists, not that the
  rate is current; see
  [docs/pricing.md](../../docs/pricing.md) for time-limited rates.
