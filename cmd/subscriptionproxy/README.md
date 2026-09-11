# subscriptionproxy

`subscriptionproxy` 是 `uniai` subscription API 的独立调用方示例。它负责自己的 token 文件、刷新锁、OAuth 登录、定时刷新和 HTTP 服务。`uniai` 库本身不管理这些状态。

程序支持三个后端：

- `codex`：ChatGPT/Codex subscription。
- `grok`：xAI Grok Build subscription。命令行也接受 `xai` 这个别名。
- `claude`：Claude subscription，通过 Claude Code OAuth 和 Messages HTTP 接口调用，不启动 `claude -p`。

## 构建

以下命令都在仓库根目录执行。构建当前系统的版本：

```bash
go build -trimpath -o subscriptionproxy ./cmd/subscriptionproxy
```

构建 Linux ARM64 版本，适用于 64 位 ARM Linux：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -trimpath -o subscriptionproxy-linux-arm64 ./cmd/subscriptionproxy
```

构建 Linux ARMv7 版本，适用于部分 32 位树莓派等设备：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -trimpath -o subscriptionproxy-linux-armv7 ./cmd/subscriptionproxy
```

构建 macOS Apple Silicon 版本：

```bash
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 \
  go build -trimpath -o subscriptionproxy-darwin-arm64 ./cmd/subscriptionproxy
```

## 登录

```bash
go run ./cmd/subscriptionproxy login \
  --backend codex \
  --token-file ./credentials/codex.json
```

```bash
go run ./cmd/subscriptionproxy login \
  --backend grok \
  --token-file ./credentials/grok.json
```

Codex 和 Grok 登录会输出设备登录地址和用户码。Claude 使用浏览器授权和 PKCE：

```bash
go run ./cmd/subscriptionproxy login \
  --backend claude \
  --token-file ./credentials/claude.json
```

打开命令输出的授权地址，完成登录后，将页面返回的 `code#state` 或包含 `code`、`state` 的完整回调 URL 粘贴回终端。登录有效期为 15 分钟，可以用 Ctrl+C 取消。不要只复制 `code`；程序会校验 `state`。

登录、状态和退出命令必须用 `--token-file` 明确指定 token 文件，不使用默认配置目录。程序不会读取 Claude Code 的本地凭据，也不会把 Codex 或 Grok 的 token 用于 Claude。

token 文件使用 `0600` 权限并通过同目录临时文件原子替换。文件内容分别是 `subscription/codex.Token`、`subscription/xai.Token` 或 `subscription/claude.Token` 的 JSON，不依赖 `uniai` 内部 store。Claude 文件包括 `access_token`、`refresh_token`、`account_id`、`expires_at` 等字段；它不是 Claude Code 自带凭据文件的格式。

查看状态和退出：

```bash
go run ./cmd/subscriptionproxy status --backend codex --token-file ./credentials/codex.json
go run ./cmd/subscriptionproxy logout --backend codex --token-file ./credentials/codex.json
go run ./cmd/subscriptionproxy status --backend claude --token-file ./credentials/claude.json
go run ./cmd/subscriptionproxy logout --backend claude --token-file ./credentials/claude.json
```

xAI logout 会先尝试调用撤销端点，然后删除本地 token。Codex 和 Claude 当前只删除本地 token，不撤销上游授权。

## HTTP 服务

一个进程可以同时加载任意两个或全部三个后端，每个后端必须使用不同的 token 文件：

```bash
go run ./cmd/subscriptionproxy serve \
  --codex-token-file ./credentials/codex.json \
  --grok-token-file ./credentials/grok.json \
  --claude-token-file ./credentials/claude.json \
  --listen 127.0.0.1:8080
```

请求中的模型按以下规则选择后端：

- `grok-` 前缀，大小写不敏感：Grok。
- `claude-` 前缀，大小写不敏感：Claude。
- 其他非空模型：Codex。

多后端模式下，请求必须提供 `model`。也可以用 `--model` 设置缺省模型，缺省模型同样按上述规则选择后端。目标后端未配置时返回错误，不会换用另一个订阅。

原来的单后端模式仍然可用。单后端模式必须显式指定缺省模型：

```bash
go run ./cmd/subscriptionproxy serve \
  --backend codex \
  --token-file ./credentials/codex.json \
  --model MODEL_ID \
  --listen 127.0.0.1:8080
```

只运行 Grok 后端：

```bash
go run ./cmd/subscriptionproxy serve \
  --backend grok \
  --token-file ./credentials/grok.json \
  --model MODEL_ID \
  --listen 127.0.0.1:8080
```

只运行 Claude 后端：

```bash
go run ./cmd/subscriptionproxy serve \
  --backend claude \
  --token-file ./credentials/claude.json \
  --model claude-sonnet-4-6 \
  --listen 127.0.0.1:8080
```

模型名称只是示例，能否调用由账号权限和上游可用模型决定。

服务提供：

- `POST /v1/chat/completions`
- `POST /v1/responses`（仅 Codex、Grok；Claude 返回 400 并提示使用 Chat Completions）
- `POST /v1/images/generations`（仅 Codex）
- `GET /healthz`

`Chat Completions` 的三个后端都支持普通 JSON 响应、工具调用和 `stream: true` 的 SSE 响应。`Responses` 的 Codex、Grok 后端支持 JSON 和 SSE。`Image Generations` 目前只支持普通 JSON 响应。这里没有提供原生 Anthropic `/v1/messages` 入站接口。

Claude 后端暂不支持 `response_format` 的 `json_object` 和 `json_schema`；这两种请求会在调用上游、开始 SSE 之前返回 400。请省略该参数或使用 `{"type":"text"}`。这里的“普通 JSON 响应”指 HTTP 响应的封装格式，不表示模型输出符合 JSON Schema。Codex、Grok 的原有参数处理不变。

Claude 的 JSON 和 SSE 响应都保留生成结束原因：`end_turn`、`stop_sequence` 转为 `finish_reason: "stop"`，`max_tokens`、`model_context_window_exceeded` 转为 `"length"`，`tool_use` 转为 `"tool_calls"`，`refusal` 转为 `"content_filter"`。工具参数被截断时仍返回 `"length"`，调用方不能把不完整参数当作可执行的工具调用。代理不自动重试截断请求。不支持的 `pause_turn` 或未知结束原因返回上游错误；若 SSE 已开始，则发送错误事件，不发送正常完成的 chunk。

Chat Completions 示例：

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "MODEL_ID",
    "messages": [{"role": "user", "content": "Say hello."}]
  }'
```

Responses 示例：

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "MODEL_ID",
    "input": "Say hello."
  }'
```

Image Generations 使用 Codex Responses 的 `image_generation` 工具。启动参数 `--model` 是调用该工具的主模型，请求体中的 `model` 是图片模型：

```bash
go run ./cmd/subscriptionproxy serve \
  --backend codex \
  --token-file ./credentials/codex.json \
  --model gpt-5.6-sol \
  --listen 127.0.0.1:8080
```

```bash
curl http://127.0.0.1:8080/v1/images/generations \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "gpt-image-2",
    "prompt": "A compact desk lamp on a plain background",
    "n": 1,
    "size": "1024x1024",
    "quality": "auto"
  }'
```

该端点返回 `data[].b64_json`，目前只支持 `n=1` 和非流式请求。它需要 Codex 后端和非空的服务端缺省主模型。订阅后端是否允许图片工具由上游账号决定。

也可以直接使用 `cmd/imagetest`：

```bash
OPENAI_API_KEY=subscription-proxy \
OPENAI_API_BASE=http://127.0.0.1:8080/v1 \
go run ./cmd/imagetest --provider openai --mode generate --openai-model gpt-image-2
```

程序启动时会读取全部已配置凭证并在必要时刷新。运行期间默认每 30 秒分别检查一次，到达 token 刷新窗口后刷新并保存；上游返回 401 时也会在对应凭据的调用方锁内刷新。可用 `--refresh-interval` 修改检查间隔。

`serve` 首次读取 token 文件后使用进程内缓存，本进程刷新 token 时会同时更新缓存和文件。如果其他进程替换或删除 token 文件，需要重启 `serve` 才会生效。

HTTP 示例没有客户端认证，默认只监听 `127.0.0.1`。不要直接暴露到公网；需要远程访问时，应在前面增加认证和 TLS。

如果登录使用了自定义 OAuth client ID，单后端 `serve` 和 xAI `logout` 需要传入同一个 `--client-id`。多后端 `serve` 分别使用 `--codex-client-id`、`--grok-client-id` 和 `--claude-client-id`。

## Claude Code 请求特征

独立模块 [`subscription/claude/claudecode`](../../subscription/claude/claudecode/request.go) 负责 HTTP 兼容处理：

- 固定上游为 `https://api.anthropic.com/v1/messages?beta=true`，用 `Authorization: Bearer` 携带 Claude access token。
- 设置 `claude-code-20250219`、`oauth-2025-04-20` beta；合并调用方额外 beta，不重复添加。
- 设置 `claude-cli/<version> (external, cli)`、`x-app: cli`、基础 Stainless 请求头、会话 ID 和每次 HTTP 尝试独立的请求 ID。默认版本为 `2.1.258`，可用 `serve --claude-code-version` 修改。
- 添加 Claude Code 身份 system 块；已有相同身份块时不重复添加。保留原 system 的层级、内容与缓存标记，不搬到 user 消息中。
- 将 `metadata.user_id` 替换为包含 `device_id`、`account_uuid`、`session_id` 的 JSON 字符串。默认设备标识由账号 ID 派生，不读取本机信息；每次 Chat 调用生成新会话 ID，401 重试保留同一个会话和请求体。
- 保留工具名称、调用 ID、参数 JSON 和工具结果，不做名称混淆或改写。
- 将 Chat Completions 的 `parallel_tool_calls: false` 映射为 `tool_choice.disable_parallel_tool_use: true`，并保留指定函数的 `tool_choice`。

库调用方可通过 `Config.ClaudeCode` 配置版本、设备 ID 和固定会话 UUID。代理不提供全局固定会话，以免把不同调用者归为同一个会话。

401 只刷新重试一次。403、429、5xx 和流内错误直接返回，不轮换账号或回退到 API key。错误不包含上游错误响应体，避免上游回显 token。Claude refresh token 轮换后若写盘失败，进程会保留新 token 并重试保存；进程退出后无法恢复仅保存在内存的 token。

这是 HTTP 层的兼容实现，不是 Claude Code 客户端的完整复刻：没有复制 TLS 指纹、billing 签名或客户端遥测，也不保证上游持续接受第三方订阅请求。修改版本号只影响 User-Agent，不能替代协议更新。请使用自己有权使用的账号，并确认服务条款与账号限制。当前验证使用离线模拟上游，未做真实账号联调。

协议常量参考 [Sub2API OAuth 实现](https://github.com/Wei-Shaw/sub2api/blob/270eac6973049fe1b50eb75560a74a029e82884c/backend/internal/pkg/oauth/oauth.go)；请求头和身份格式参考 [CLIProxyAPI 请求实现](https://github.com/router-for-me/CLIProxyAPI/blob/7fac6b15bcfe5ea55c18c9eaec8e5b7e6457d974/internal/runtime/executor/claude_executor_request.go) 与 [账号身份实现](https://github.com/router-for-me/CLIProxyAPI/blob/7fac6b15bcfe5ea55c18c9eaec8e5b7e6457d974/internal/runtime/executor/helps/claude_credential_identity.go)。这些是第三方实现记录，不是稳定的官方 API 承诺。
