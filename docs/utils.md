# Utility Functions

This document describes exported helper functions in `uniai` for working with JSON-like outputs.

Import:

```go
import "github.com/quailyquaily/uniai"
```

## StripNonJSONLines

```go
cleaned := uniai.StripNonJSONLines(text)
```

Removes lines that are unlikely to be part of a JSON payload. When not already inside a JSON block,
any line that does not start with `{` or `[` and does not contain `{` or `[` within the next 20
characters is dropped. Multi-line JSON blocks are kept intact by tracking brace/bracket depth.

Example:

Input:
```text
> thought line
some preface
{"tools":[
  {"tool":"get_weather","arguments":{"city":"Tokyo"}}
]}
```

Output:
```text
{"tools":[
  {"tool":"get_weather","arguments":{"city":"Tokyo"}}
]}
```

## AttemptJSONRepair

```go
repaired := uniai.AttemptJSONRepair(text)
```

Applies minimal repairs for common JSON issues:

- Removes trailing commas before `}` or `]`.
- Closes an unterminated JSON string by appending `"`.
- Adds missing closing braces/brackets based on simple counts.

It does **not** fix structural JSON errors such as missing quotes around keys.

Example:

Input:
```text
{"tool":"get_weather","arguments":{"city":"Tokyo",},}
```

Output:
```text
{"tool":"get_weather","arguments":{"city":"Tokyo"}}
```

## FindJSONSnippets

```go
snippets := uniai.FindJSONSnippets(text)
```

Scans the input and returns all valid JSON substrings it can find. Useful when the response contains
extra text before/after JSON.

Example:

Input:
```text
prefix {"a":1} middle [{"b":2}] suffix
```

Output:
```text
["{\"a\":1}", "[{\"b\":2}]"]
```

## CollectJSONCandidates

```go
candidates, err := uniai.CollectJSONCandidates(text)
```

Collects possible JSON payloads from:

- The full input text
- Markdown code fences
- Embedded JSON snippets
- JSON wrapped as a string literal

Returns candidates in order of appearance.

Example:

Input:
````text
Here is JSON:
```json
{"value":1}
```
And another one: {"value":2}
````

Output (candidates, order of appearance; duplicates possible):
```text
- full input text
- {"value":1}
- {"value":2}
```
