# Cloudflare Workers AI support prep (2026-02-06)

## Scope (confirmed)
- Use Cloudflare Workers AI REST API (`/ai/run/{model}`), not OpenAI-compatible endpoints.
- Support: text (LLM), embeddings, image (Flux), audio (Whisper ASR).
- Keep model IDs out of config; model must be provided per request.
- Provide common field mapping + provider options JSONMap for model-specific inputs.
  - Text default choice: `@cf/openai/gpt-oss-120b` (gpt-oss family).

## References
- https://developers.cloudflare.com/workers-ai/
- https://developers.cloudflare.com/workers-ai/get-started/rest-api/
- https://developers.cloudflare.com/workers-ai/features/prompting/
- https://developers.cloudflare.com/workers-ai/models/ (for gpt-oss / qwen / deepseek / flux / whisper model schemas)

## Key API surfaces
- REST API: `POST https://api.cloudflare.com/client/v4/accounts/{ACCOUNT_ID}/ai/run/{model}` with `Authorization: Bearer {API_TOKEN}`.
- Response envelope: `{ success, errors, messages, result }`.
- Model input schemas are model-specific. Text models commonly accept `prompt` or `messages` (scoped prompts), embeddings accept `text`, Flux returns base64 `image`, Whisper returns `text` plus `segments`.
- gpt-oss models use a Responses-style input/output when invoked via `/ai/run`.

## uniai design notes
- Provider name: `cloudflare`.
- Config additions:
  - `CloudflareAccountID`
  - `CloudflareAPIToken`
  - `CloudflareAPIBase` (default `https://api.cloudflare.com/client/v4`)
- No default model in config; require `req.Model`.

## Mapping strategy
- Text:
  - If model is gpt-oss: map `chat.Messages` to Responses-style `input` array (role/content).
  - Else: map `chat.Messages` to `messages` (scoped prompts).
  - Set common parameters (`temperature`, `top_p`, `max_tokens`, `stop`, penalties) only if not already provided in `options.cloudflare`.
  - If `options.cloudflare` includes `prompt/messages/input`, skip auto-mapping.
- Embeddings:
  - Map inputs to `text` (string or array) unless `options.cloudflare` already sets it.
  - Convert returned float arrays to base64 to match existing `embedding.Result` format.
- Images (Flux):
  - Map prompt + `num_images` (from count) unless overridden in options.
  - If model name contains `flux-2-` or `options.cloudflare.multipart=true`, send multipart form-data; otherwise JSON.
  - Extract base64 image(s) from `result.image` or `result.images`.
- Audio (Whisper ASR):
  - Map audio base64 to `audio` unless overridden.
  - Extract `text`, `language`, and `segments` when present.

## Implementation checklist
1. Add `internal/providers/cloudflare` helpers to call `/ai/run/{model}` and parse the Cloudflare response envelope.
2. Add `providers/cloudflare` chat provider with mapping + JSONMap passthrough.
3. Extend embeddings & images to route to Cloudflare internal provider.
4. Add `audio/` package + `Client.Audio()` for Whisper transcription.
5. Add `Cloudflare*` config + defaults; update exports.
6. Update README + env example.

## Open questions / future work
- Streaming support for `/ai/run` (SSE) is not included in v1.
- TTS can be added later by extending the audio module.
