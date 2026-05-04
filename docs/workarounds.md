# Workarounds

Use the narrowest stable trigger available, prefer preserving provider metadata, and add tests for both hit and miss cases.

## OpenAI-Compatible Reasoning Content

- Paths:
  - `chat/types.go`
  - `internal/oaicompat/convert.go`
  - `internal/oaicompat/result.go`
- Trigger:
  - an assistant `chat.Message` has non-empty `ReasoningContent`
- Behavior:
  - preserve provider-returned `reasoning_content` on `Result.Messages`
  - pass `Message.ReasoningContent` back as `reasoning_content` in OpenAI-compatible Chat Completions requests
- Notes:
  - this is not limited to Kimi or DeepSeek; if an OpenAI-compatible provider returns this field, replay should keep it
  - no placeholder `reasoning_content` is injected
  - callers that maintain conversation history should append `chat.AssistantReplayMessages(resp)`, not rebuild assistant history from `resp.ToolCalls`
- Tests:
  - `providers/openai/openai_test.go`
  - `internal/oaicompat/stream_test.go`

Recommended pattern:

```go
messages := []chat.Message{chat.User("read README.md")}

resp, err := client.Chat(ctx, &chat.Request{
	Provider: "deepseek",
	Messages: messages,
	Tools:    tools,
})
if err != nil {
	return err
}

toolMsg, err := chat.ToolResultValue(resp.ToolCalls[0].ID, map[string]any{
	"content": "...file text...",
})
if err != nil {
	return err
}

messages = append(messages, chat.AssistantReplayMessages(resp)...)
messages = append(messages, toolMsg)

_, err = client.Chat(ctx, &chat.Request{
	Provider: "deepseek",
	Messages: messages,
	Tools:    tools,
})
```

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
  - `chat.AssistantReplayMessages(resp)`
  - `chat.ToolResultValue(call.ID, value)`
- Behavior:
  - `AssistantReplayMessages` replays the assistant turn while preserving provider-specific metadata
  - `ToolResultValue` keeps object JSON unchanged
  - non-object values are wrapped as `{"result": ...}`
  - for parallel Gemini function calls in the same assistant turn, only the first call needs `thought_signature`
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

messages := []chat.Message{chat.User("read README.md")}
messages = append(messages, chat.AssistantReplayMessages(resp)...)
messages = append(messages, toolMsg)

_, err = client.Chat(ctx, &chat.Request{
	Messages: messages,
	Tools: tools,
})
```

- Tests:
  - `chat/types_test.go`
  - `providers/gemini/gemini_test.go`
