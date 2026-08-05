# OpenAI Codex Responses Provider（2026-08-05）

## 状态

- Implemented
- Provider 名称：`openai_codex`

## 目标

`openai_codex` 使用和 `openai_resp` 相同的 `/v1/responses` 传输、流式解析和响应转换，只调整发送给上游的请求字段。

```go
resp, err := client.Chat(ctx,
	uniai.WithProvider("openai_codex"),
	uniai.WithModel("gpt-5.3-codex"),
	uniai.WithMessages(uniai.User("Explain this change.")),
	uniai.WithReasoningEffort(uniai.ReasoningEffortHigh),
)
```

复用现有配置：

- `Config.OpenAIAPIKey`
- `Config.OpenAIAPIBase`
- `Config.OpenAIModel`
- `Config.ChatHeaders`

除本文列出的请求差异外，行为都以 `openai_resp` 为准。不新增认证、响应类型、流式入口或 Codex 专用模型列表。

## 请求规则

| 调用侧输入 | `openai_codex` 发出的请求 |
| --- | --- |
| `WithTemperature(...)` | 不发送 `temperature` |
| `openai_options.temperature` | 删除 |
| `WithMaxTokens(...)` | 不发送 `max_output_tokens` |
| `openai_options.max_tokens` | 删除 |
| `openai_options.max_output_tokens` | 删除 |
| `prompt_cache_key` | 删除 |
| `prompt_cache_retention` | 删除 |
| `prompt_cache_options` | 删除 |
| input 中任意深度的 `prompt_cache_breakpoint` | 删除该字段，保留同一 content block 的其他字段 |
| message part 或 tool 上的 `CacheControl` | 忽略，不生成 breakpoint |
| `WithReasoningBudgetTokens(...)` | 忽略，不报错 |
| `openai_options.reasoning_budget_tokens` | 删除 |
| `WithReasoningEffort(...)` | 保留为 `reasoning.effort` |
| `openai_options.reasoning.effort` | 保留 `openai_resp` 的现有处理 |

这些规则与模型名无关。被删除或忽略的字段不产生 warning，也不触发 `openai_resp` 原有的 unsupported-field 错误。其他字段仍使用 `openai_resp` 的校验和映射。

这里必须同时处理公共 options 和 `WithOpenAIOptions(...)`。例如，`WithMaxTokens(...)` 来自 `chat.Options.MaxTokens`，而 `max_output_tokens` 也可能直接出现在原始 OpenAI options 中。

关闭的是显式缓存控制。上游仍可自行执行隐式 prompt caching；返回的 cache usage 继续按现有逻辑解析。

## JSON 模式

以下任一种输入表示 JSON object mode：

```json
{ "response_format": "json_object" }
```

```json
{ "response_format": { "type": "json_object" } }
```

```json
{ "text": { "format": { "type": "json_object" } } }
```

`response_format` 是 `uniai` 已有的兼容输入名。`/v1/responses` 的最终网络请求使用官方字段：

```json
{
  "text": {
    "format": {
      "type": "json_object"
    }
  }
}
```

不发送 Responses API 未定义的顶层 `response_format`。

JSON object mode 要求模型可见上下文包含区分大小写的字符串 `JSON`。检查范围只包括 `instructions` 和 input 中的文本内容。若不存在，在 input 最前面增加一条 system message：

```json
{
  "role": "system",
  "content": "Return the response as JSON."
}
```

已有 `JSON` 时不增加消息。原 input 的文本和相对顺序保持不变。

`json_schema` 属于 Structured Outputs，不改写成 `json_object`，也不增加上述 system message。

## 实现

`openai_codex` 不是新的协议实现：

1. 根 client 增加 `openai_codex` 路由。
2. 路由仍创建 `providers/openai_resp.Provider`，并传入一个内部 Codex compatibility 标记。
3. Responses 参数 builder 根据该标记执行本文的请求规则。
4. HTTP 请求、blocking、streaming、tool calls、reasoning details、usage 和响应解析继续使用现有代码。

不新增独立的 `providers/openai_codex` package，也不新增通用 request policy、middleware 或 transform pipeline。

Builder 本来就会创建新的 Responses 参数，因此不需要克隆完整的 `chat.Request`：

- 不支持的公共 options 直接不写入参数。
- 原始 `OpenAI` options 只复制需要过滤的 map，不能原地删除调用方数据。
- raw input 在解码后的副本中递归删除 `prompt_cache_breakpoint`。
- message 和 tool 的 `CacheControl` 在构建 input/tools 时直接忽略。
- JSON system message 加入新建的 Responses input，不回写 `req.Messages`。

`openai_resp` 不启用该标记，现有行为保持不变，包括其 reasoning budget 错误和 prompt cache 支持。

## 测试

实现前先添加测试，覆盖：

1. 请求测试覆盖 temperature、两种 token limit、reasoning budget、reasoning effort 和三个顶层 cache 字段。
2. raw input 测试覆盖嵌套 `prompt_cache_breakpoint`，并确认其他字段未改变。
3. cache control 测试确认 message part 和 tool 不生成 breakpoint。
4. JSON mode 测试覆盖三种输入形式、已有或缺少 `JSON`、以及不受影响的 `json_schema`。
5. 不可变性测试确认传入的 options、messages、parts 和 tools 没有被修改。
6. 路由测试确认 `WithProvider("openai_codex")` 请求 `/v1/responses`。
7. 回归测试确认 `openai_resp` 仍保留原有字段处理。

不重复测试 blocking、streaming 和 reasoning event 的完整行为。这些代码没有分叉，已有 `openai_resp` 测试继续负责它们。

验收命令：

```bash
go test ./...
```

## 官方参考

- Responses API create：<https://developers.openai.com/api/reference/resources/responses/methods/create>
- Structured Outputs 与 JSON mode：<https://developers.openai.com/api/docs/guides/structured-outputs#json-mode>

JSON mode 的 `text.format` 和大写 `JSON` 要求来自 OpenAI Responses API。删除 cache、token limit、temperature 和 reasoning budget 是 `openai_codex` 的兼容策略，不改变 `openai_resp`。
