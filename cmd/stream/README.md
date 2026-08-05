# stream reasoning test

`cmd/stream` is a live integration test for normalized reasoning events from
`uniai.Chat`.

The command always enables both:

- `WithReasoningDetails()`
- `WithOnStream(...)`

A test passes only when the stream callback receives at least one non-empty
`ReasoningDelta` and a final `Done` event. A reasoning value found only in the
final response does not pass the test.

The example config contains these live cases:

| Test | Provider route | Model | Expected reasoning type |
| --- | --- | --- | --- |
| `deepseek_v4_pro` | `deepseek` | `deepseek-v4-pro` | `thinking` |
| `kimi_k3` | `openai` with Moonshot API base | `kimi-k3` | `thinking` |
| `anthropic_claude_sonnet_5` | `anthropic` | `claude-sonnet-5` | `thinking` |
| `openai_gpt_5_6_luna` | `openai_resp` | `gpt-5.6-luna` | `summary` |

## Run

Copy the environment template and provide the keys needed by your selected
tests:

```bash
cp cmd/stream/env.example.sh .env.stream.sh
source .env.stream.sh
```

Run all configured tests:

```bash
go run ./cmd/stream --config cmd/stream/config.example.yaml run
```

Run one named test:

```bash
go run ./cmd/stream \
  --config cmd/stream/config.example.yaml \
  run kimi_k3
```

The command exits with a non-zero status when a request fails, no non-empty
reasoning delta is observed, or the stream has no final `Done` event.

## Config

```yaml
prompt: "Find the smallest positive integer divisible by every integer from 1 through 12. Give the result and a brief verification."
max_tokens: 2048
timeout_seconds: 180

tests:
  - name: kimi_k3
    provider: openai
    api_base: https://api.moonshot.ai/v1
    api_key_ref: MOONSHOT_API_KEY
    model: kimi-k3
    reasoning_effort: high

  - name: anthropic_claude_sonnet_5
    provider: anthropic
    api_key_ref: ANTHROPIC_API_KEY
    model: claude-sonnet-5
    reasoning_effort: high
    max_tokens: 8192

  - name: openai_gpt_5_6_luna
    provider: openai_resp
    api_key_ref: OPENAI_API_KEY
    model: gpt-5.6-luna
    reasoning_effort: high
    max_tokens: 32768
```

`api_key_ref` contains the name of an environment variable, not the API key.
The command reads the referenced variable immediately before creating the
client. It never writes the secret to output.

Top-level fields provide defaults for all tests:

- `prompt`
- `max_tokens`; default `2048`
- `timeout_seconds`; default `180`

Each `tests[]` item accepts:

- `name`: unique test name
- `provider`: defaults to `openai`
- `api_base`: optional provider endpoint
- `api_key_ref`: required environment variable name
- `model`: required model name
- `prompt`: optional per-test prompt override
- `max_tokens`: optional per-test override
- `timeout_seconds`: optional per-test override
- `reasoning_effort`: `none`, `minimal`, `low`, `medium`, `high`, `max`, or
  `xhigh`
- `reasoning_budget_tokens`: integer reasoning budget

`reasoning_effort` and `reasoning_budget_tokens` are mutually exclusive.
Provider-specific validation remains in `uniai`; for example, Anthropic models
that use manual thinking require a reasoning budget.

The command can construct API-key configs for these providers:

- `openai` for OpenAI-compatible Chat Completions endpoints, including Kimi
- `deepseek`
- `openai_resp`
- `openai_codex`
- `gemini`
- `anthropic`
- `sakana`, `xai`, `groq`, and `meta`

Azure, Bedrock, and Cloudflare use credential shapes other than one API key and
are not configured by this command.

Credential support does not imply reasoning support. The test only passes when
the selected provider and model actually stream a readable reasoning delta.

## Output

Reasoning and answer text are streamed directly under separate headings. Chunk
boundaries do not appear in the text:

```text
kimi_k3 (openai / kimi-k3)

Reasoning:
...

Answer:
...

Usage: input 20, output 45, total 65 tokens
PASS: reasoning 341 chars (12 chunks), answer 57 chars (3 chunks), elapsed 1.234s
```

The DeepSeek example uses the current V4 model name and its native DeepSeek
route. The Kimi example uses Moonshot's OpenAI-compatible endpoint. See the
[DeepSeek thinking-mode documentation](https://api-docs.deepseek.com/guides/thinking_mode/)
and the [Kimi K3 model documentation](https://github.com/MoonshotAI/Kimi-K3#6-model-usage).
Claude Sonnet 5 uses adaptive thinking with effort; GPT-5.6 Luna uses the
Responses API with reasoning summaries. See the
[Claude Sonnet 5 documentation](https://platform.claude.com/docs/en/about-claude/models/whats-new-sonnet-5)
and the [GPT-5.6 Luna model page](https://developers.openai.com/api/docs/models/gpt-5.6-luna).
