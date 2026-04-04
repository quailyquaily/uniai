# Workarounds

Use the narrowest stable trigger available, prefer preserving provider metadata, and add tests for both hit and miss cases.

## Kimi / Moonshot

- Path: `providers/openai/openai.go`
- Trigger:
  - model starts with `kimi-`
  - or base URL host is `api.moonshot.ai`, `api.kimi.com`, or `api.moonshot.cn`
- Behavior: inject `reasoning_content: "."` on assistant messages with `tool_calls`
- Notes:
  - only applies to the `openai` provider path
  - does not apply to `openai_resp`
- Tests: `providers/openai/openai_test.go`

## Gemini OpenAI-Compatible

- Path: `internal/oaicompat/convert.go`
- Trigger:
  - model starts with `gemini-`
  - or model contains `/gemini-`
- Behavior:
  - preserve `ThoughtSignature` when present
  - otherwise inject `extra_content.google.thought_signature: "skip_thought_signature_validator"`
- Notes:
  - shared in `internal/oaicompat` because multiple OpenAI-compatible paths use the same mapper
  - native Gemini is stricter and errors if `thought_signature` is missing
- Tests: `providers/openai/openai_test.go`
