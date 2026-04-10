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

## Gemini Native Tool Replay

- Paths:
  - `chat/types.go`
  - `providers/gemini/gemini.go`
- Problem:
  - replaying assistant tool calls must preserve Gemini `thought_signature`
  - `function_response.response` is safest as a JSON object
- Helpers:
  - `chat.AssistantToolCalls(resp.ToolCalls...)`
  - `chat.ToolResultValue(call.ID, value)`
- Behavior:
  - `AssistantToolCalls` replays the prior tool calls as-is
  - `ToolResultValue` keeps object JSON unchanged
  - non-object values are wrapped as `{"result": ...}`
- Recommended pattern:

```go
resp, err := client.Chat(ctx, &chat.Request{
	Messages: []chat.Message{chat.User("read README.md")},
	Tools:    tools,
})
if err != nil {
	return err
}

call := resp.ToolCalls[0]
toolMsg, err := chat.ToolResultValue(call.ID, map[string]any{
	"content": "...file text...",
})
if err != nil {
	return err
}

_, err = client.Chat(ctx, &chat.Request{
	Messages: []chat.Message{
		chat.User("read README.md"),
		chat.AssistantToolCalls(resp.ToolCalls...),
		toolMsg,
	},
	Tools: tools,
})
```

- Tests:
  - `chat/types_test.go`
  - `providers/gemini/gemini_test.go`
