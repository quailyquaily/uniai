# openairesptest

Small repro/demo for the GPT-5.4 reasoning + function-tools split between:

- `openai` -> Chat Completions
- `openai_resp` -> Responses API

It covers the OpenAI error:

`Function tools with reasoning_effort are not supported for gpt-5.4 in /v1/chat/completions. Please use /v1/responses instead.`

## Run

```bash
OPENAI_API_KEY=... go run ./cmd/openairesptest --model gpt-5.4
```

What it does:

1. Sends a `gpt-5.4` request with `reasoning_effort` and function tools through provider `openai`
2. Verifies the expected Chat Completions failure
3. Sends the same scenario through provider `openai_resp`
4. Executes the returned tool call
5. Continues with `previous_response_id` until a final answer is produced

Useful flags:

- `--skip-openai`
- `--skip-openai-resp`
- `--prompt`
- `--timeout`
