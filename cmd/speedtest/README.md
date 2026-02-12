# speedtest CLI

`cmd/speedtest` is a standalone CLI for running simple latency checks against multiple LLM endpoints using `uniai`.

Each test case runs **3 attempts** and reports:

- per-attempt duration
- pass/fail status
- average duration

Supported methods:

- `echo` (default): checks exact text echo
- `toolcalling`: checks whether the model selects the expected tool

## Run

Run all tests:

```bash
go run ./cmd/speedtest --config cmd/speedtest/config.example.yaml run
```

Run one named test:

```bash
go run ./cmd/speedtest --config cmd/speedtest/config.example.yaml run openai_gpt_5.2
```

Run with tool-calling method:

```bash
go run ./cmd/speedtest --config cmd/speedtest/config.example.yaml --method toolcalling run
```

Write CSV to a custom file:

```bash
go run ./cmd/speedtest --config cmd/speedtest/config.example.yaml --output ./out/speedtest_toolcalling.csv --method toolcalling run
```

## CLI Flags

- `--config`: path to YAML config file. Default: `config.yaml`
- `--output`: output CSV path. Default: `speedtest_results.csv`
- `--csv`: deprecated alias of `--output`
- `--method`: `echo` or `toolcalling`. Default: `echo`

Command form:

```bash
go run ./cmd/speedtest [flags] run [test_name]
```

- omit `test_name` to run all tests
- include `test_name` to run only one test from `tests[]`

## Config File

See `cmd/speedtest/config.example.yaml`.

Example:

```yaml
temperature: 0
echo_text: speedtest-echo-20260211
timeout_seconds: 90

tests:
  - name: "openai_gpt_5.2"
    provider: openai
    api_base: ""
    api_key_ref: OPENAI_API_KEY
    model: gpt-5.2

  - name: "openrouter_openai_gpt_5.2"
    provider: openai
    api_base: https://openrouter.ai/api/v1
    api_key_ref: OPENROUTER_API_KEY
    model: openai/gpt-5.2

  - name: "cloudflare_gpt_oss_120b"
    provider: cloudflare
    api_base: ""
    cloudflare_account_id_ref: CLOUDFLARE_ACCOUNT_ID
    cloudflare_api_token_ref: CLOUDFLARE_API_TOKEN
    model: "@cf/openai/gpt-oss-120b"
```

### Top-Level Fields

- `model`: default model for all tests (optional)
- `temperature`: default temperature (optional, default `1.0`)
- `echo_text`: default echo text (optional, default `speedtest-echo-20260211`)
- `timeout_seconds`: default timeout in seconds (optional, default `90`)
- `tests`: list of test entries (required)

### `tests[]` Fields

- `name`: test name (required, must be unique)
- `provider`: provider name (optional)
  - default is `openai_custom` when `api_base` is non-empty
  - otherwise default is `openai`
- `api_base`: API base URL (optional)
- `api_key_ref`: environment variable name for API key (required for non-cloudflare providers)
- `cloudflare_account_id_ref`: env var name for Cloudflare Account ID (required when `provider=cloudflare`)
- `cloudflare_api_token_ref`: env var name for Cloudflare API Token (required when `provider=cloudflare`; falls back to `api_key_ref` if omitted)
- `model`: model name (optional in test; falls back to top-level `model`)
- `temperature`: per-test override (optional)
- `echo_text`: per-test override (optional)
- `timeout_seconds`: per-test override (optional)

## Methods

### `echo`

- Input: `echo_text`
- Match rule: response text must be exactly equal to input text (byte-for-byte)

### `toolcalling`

- Injected mock tools:
  - `get_weather`
  - `get_direction`
  - `send_message`
- Fixed prompt:
  - `从 tokyo station 到 shinjuku station 怎么走`
- Match rule:
  - any returned tool call named `get_direction` is treated as success

## Environment Variables

Use `cmd/speedtest/env.example.sh` as a template:

```bash
source cmd/speedtest/env.example.sh
# then fill real values
```

For non-cloudflare providers:

- `api_key_ref` must point to a non-empty env var

For `provider: cloudflare`:

- `cloudflare_account_id_ref` must point to a non-empty env var (for example `CLOUDFLARE_ACCOUNT_ID`)
- `cloudflare_api_token_ref` (or `api_key_ref`) must point to a non-empty env var (for example `CLOUDFLARE_API_TOKEN`)

## Output

The CLI prints both terminal output and CSV.

Terminal output includes:

- test header (provider/model/api base/key ref)
- 3 attempt rows
- average latency
- setup/runtime errors

CSV columns:

- `test_name`
- `provider`
- `model`
- `attempt`
- `duration_ms`
- `ok`
- `echo_match`
- `average_ms`

`attempt` values:

- `1|2|3`: regular attempts
- `avg`: average row
- `setup`: setup failure row (for example missing model or missing env var)

## Troubleshooting

If you see errors mentioning `text/html` or `not 'application/json'`, the endpoint is likely wrong.

For OpenRouter, use:

- `https://openrouter.ai/api/v1`
