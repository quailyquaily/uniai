# Provider Compatibility Workarounds

## Purpose

`uniai` tries to keep the public API provider-neutral, but some model families exposed through "OpenAI-compatible" APIs still require request shapes that are not part of the normal OpenAI schema.

This document records those exceptions and the rules for implementing them.

## Policy

When a provider or model needs a compatibility workaround:

1. Keep the workaround in the narrowest provider path that can enforce it.
2. Trigger it with the narrowest stable predicate available.
3. Prefer preserving provider-returned metadata over inventing new values.
4. If a synthetic value is unavoidable, document it and add tests for hit and miss cases.
5. Do not push provider-specific hacks into shared abstractions unless multiple providers genuinely need the same behavior.

In practice this usually means:

- put OpenAI-compatible request hacks in `providers/openai/`, not in generic `chat` types
- scope the behavior by exact host, provider family, or model prefix
- add request-build tests that verify the serialized payload, not just in-memory structs

## Current Workarounds

### Kimi / Moonshot `reasoning_content` on assistant tool-call messages

Scope:

- provider: `openai`
- API style: Chat Completions / OpenAI-compatible request path
- implementation: [`providers/openai/openai.go`](/home/lyric/Codework/arch/uniai/providers/openai/openai.go)

Trigger:

- request model starts with `kimi-`
- or request base URL host is one of:
  - `api.moonshot.ai`
  - `api.kimi.com`
  - `api.moonshot.cn`

Behavior:

- for assistant messages that contain `tool_calls`, inject `reasoning_content: "."`

Reason:

- Kimi's thinking-enabled tool-calling flow may reject assistant tool-call replay messages that omit `reasoning_content`, even on an otherwise OpenAI-compatible endpoint

Notes:

- this workaround is intentionally limited to the `openai` provider path
- it does not apply to `openai_resp`
- the injected `"."` value is a compatibility placeholder, not reconstructed reasoning content

Tests:

- [`providers/openai/openai_test.go`](/home/lyric/Codework/arch/uniai/providers/openai/openai_test.go) covers:
  - trigger by model prefix
  - trigger by compatible base URL
  - no injection for non-Kimi requests

## Maintenance

Before adding a new workaround entry:

1. Confirm the behavior is provider- or model-specific, not a bug in `uniai`'s generic mapping.
2. Check whether the provider requires exact replay of metadata from prior turns.
3. Add or update this document in the same change.
4. Include removal criteria in the PR description if the workaround is expected to be temporary.
