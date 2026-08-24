# subscriptionproxy

`subscriptionproxy` 是 `uniai` subscription API 的独立调用方示例。它负责自己的 token 文件、刷新锁、设备登录、定时刷新和 HTTP 服务。`uniai` 库本身不管理这些状态。

程序支持两个后端：

- `codex`：ChatGPT/Codex subscription。
- `grok`：xAI Grok Build subscription。命令行也接受 `xai` 这个别名。

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

命令会输出设备登录地址和用户码。登录、状态和退出命令必须用 `--token-file` 明确指定 token 文件，不使用默认配置目录。

token 文件使用 `0600` 权限并通过同目录临时文件原子替换。文件内容分别是 `subscription/codex.Token` 或 `subscription/xai.Token` 的 JSON，不依赖 `uniai` 内部 store。

查看状态和退出：

```bash
go run ./cmd/subscriptionproxy status --backend codex --token-file ./credentials/codex.json
go run ./cmd/subscriptionproxy logout --backend codex --token-file ./credentials/codex.json
```

xAI logout 会先尝试调用撤销端点，然后删除本地 token。Codex 当前只删除本地 token。

## HTTP 服务

一个进程可以同时加载两套凭据：

```bash
go run ./cmd/subscriptionproxy serve \
  --codex-token-file ./credentials/codex.json \
  --grok-token-file ./credentials/grok.json \
  --listen 127.0.0.1:8080
```

请求中的模型按以下规则选择后端：

- `grok-` 前缀，大小写不敏感：Grok。
- 其他非空模型：Codex。

双后端模式下，请求必须提供 `model`。也可以用 `--model` 设置缺省模型，缺省模型同样按上述规则选择后端。

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

服务提供：

- `POST /v1/chat/completions`
- `POST /v1/responses`
- `GET /healthz`

两个模型接口都支持普通 JSON 响应和 `stream: true` 的 SSE 响应。

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

程序启动时会读取全部已配置凭证并在必要时刷新。运行期间默认每 30 秒分别检查一次，到达 token 刷新窗口后刷新并保存；上游返回 401 时也会在对应凭据的调用方锁内刷新。可用 `--refresh-interval` 修改检查间隔。

`serve` 首次读取 token 文件后使用进程内缓存，本进程刷新 token 时会同时更新缓存和文件。如果其他进程替换或删除 token 文件，需要重启 `serve` 才会生效。

HTTP 示例没有客户端认证，默认只监听 `127.0.0.1`。不要直接暴露到公网；需要远程访问时，应在前面增加认证和 TLS。

如果登录使用了自定义 OAuth client ID，单后端 `serve` 和 xAI `logout` 需要传入同一个 `--client-id`。双后端 `serve` 分别使用 `--codex-client-id` 和 `--grok-client-id`。
