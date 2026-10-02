# Counting Input Tokens

`Client.CountTokens(ctx, opts ...chat.Option)` sends an input-counting request to
an upstream provider. It returns `*TokenCount` with `InputTokens` and `Model`.
It does not run inference, invoke streaming callbacks, or record `Usage` or
`Usage.Cost`.

## Usage

Use the same options as the intended Chat request:

```go
opts := []chat.Option{
    chat.WithProvider("anthropic"),
    chat.WithModel("claude-sonnet-5-5"),
    chat.WithMessages(chat.System("Be concise."), chat.User("Explain a mutex.")),
}
count, err := client.CountTokens(ctx, opts...)
if errors.Is(err, uniai.ErrTokenCountUnsupported) {
    // Use the application's own estimate.
} else if err != nil {
    return err
} else {
    fmt.Println(count.InputTokens, count.Model)
}
```

An omitted provider uses `Config.Provider`, then `openai`. The `openai` provider
uses Chat Completions and does not support this operation. Select `openai_resp`
for an intended Responses API request.

`client.SupportsCountTokens(provider)` checks whether uniai has a counting
adapter. It makes no network request and does not guarantee model, region,
account, input, or proxy support. An empty provider selects the configured default.

## Providers

| Provider | Counting operation | Configuration |
| --- | --- | --- |
| `anthropic` | `POST {AnthropicAPIBase}/messages/count_tokens` | Same key, base and default model as Chat |
| `gemini` | `POST {GeminiAPIBase}/v1beta/models/{model}:countTokens` | Same key, normalized base and default model as Chat; retains the OpenAI key/model fallback |
| `openai_resp` | `POST {OpenAIAPIBase}/responses/input_tokens` | Same key, base and default model as Chat |
| `openai_codex` with an API key | Responses input-token counting with Codex request adaptation | Subscription credentials are unsupported |
| `bedrock` | Bedrock Runtime `CountTokens` with the native `InvokeModel` body | Same AWS credentials, region and `AwsBedrockModelArn` as Chat |

`Config.ChatHeaders` also applies to counting. `ModelsHeaders` and
`ModelsHTTPClient` apply only to listing models. No SDK upgrade is required.
Native Anthropic and Gemini counting requests reject redirects to another
scheme, host or port to keep credentials on the configured origin.

Other providers return an error wrapping `ErrTokenCountUnsupported`. An
OpenAI-compatible Chat Completions service is not assumed to implement the
Responses counting endpoint.

Bedrock availability depends on the model and region. Some models require the
separate Bedrock Mantle endpoint, which this adapter does not implement. Bedrock
tools are unsupported because the existing Chat adapter does not map them.

## Request Construction and Partial Inputs

Counting reuses the provider's Chat input conversion, including system prompts,
tools, images, cache markers, model-specific options and history replay. Gemini
sends the complete `generateContentRequest`; OpenAI and Anthropic send the input
fields accepted by their counting endpoints. Generation-only fields are omitted
where that endpoint does not accept them.

Counting permits an empty message list, a system prompt alone, tools alone, or a
prefix of the history. It never pads the request with synthetic messages. It
still validates message parts, tool schemas and cache controls. The upstream
endpoint may reject a particular partial input; that error is returned to the
caller. Chat continues to require a valid inference request.

Count growing prefixes of the same request to estimate each part's contribution.
Keep the model and other options fixed. Differences between prefix counts are
context-dependent estimates, not exact billing allocations.

The following request modes return `ErrTokenCountUnsupported`:

- Tool emulation with tools: a Chat call may construct and run multiple requests.
- OpenAI stored `prompt` templates: the counting endpoint cannot resolve them.
- Subscription authentication.

OpenAI raw `input`, instructions, response formats, tool definitions,
`previous_response_id` and `conversation` are retained. A count includes the
context resolved by the upstream service for those identifiers.

## Errors and Accuracy

Use `errors.Is(err, uniai.ErrTokenCountUnsupported)` for known unsupported
adapters or request modes, upstream HTTP 405/501 responses, and Bedrock validation
errors that explicitly report unsupported token counting. Authentication,
permission, rate-limit, network and other validation errors remain errors; they
are not silently replaced with a local estimate. A 404 is not automatically
classified as unsupported because it can indicate an invalid model.

Missing, negative or malformed token counts are errors. A reported count of zero
is valid.

Input counting does not predict cache hits, cache-write TTL costs or output
usage. Anthropic explicitly describes its count as an estimate that may differ
from actual usage. Use the final Chat `Usage` for accounting. Counter results are
never accumulated into Chat usage or cost.

## References

- [Anthropic token counting](https://platform.claude.com/docs/en/build-with-claude/token-counting)
- [Gemini counting API](https://ai.google.dev/api/tokens)
- [OpenAI token counting](https://developers.openai.com/api/docs/guides/token-counting)
- [Bedrock token counting](https://docs.aws.amazon.com/bedrock/latest/userguide/count-tokens.html)
