# Codex 与 xAI Subscription 支持（2026-08-24）

## 状态

- Implemented
- Codex provider：`openai_codex`
- xAI subscription provider：`xai_oauth`

## 目标

让调用方通过 `uniai` 使用 Codex subscription 和 Grok Build subscription，同时保持 `uniai` 的无状态库边界。

`uniai` 负责：

- Codex 与 xAI 的 OAuth HTTP 协议。
- 将订阅凭证用于模型请求。
- Codex 与 xAI subscription 的请求兼容、响应解析和流式处理。
- HTTP 401 后获取新凭证并重试一次。
- 订阅请求的 usage 与费用语义。

调用方负责：

- token 持久化。
- token 文件路径、文件权限、数据库或系统密钥环。
- 过期判断、刷新并发控制和刷新结果保存。
- 登录界面、浏览器跳转、设备码轮询节奏和状态展示。
- 退出登录后的本地状态清理。

`uniai` 不提供 Store、Session、默认 token 路径或登录 CLI。

## 设计边界

```text
调用方
  ├─ 登录界面
  ├─ token store
  ├─ 刷新并发控制
  └─ CredentialSource
          │
          ▼
uniai
  ├─ subscription/codex：无状态 OAuth 函数
  ├─ subscription/xai：无状态 OAuth 函数
  ├─ providers/codex：Codex subscription 请求
  └─ providers/xaioauth：xAI subscription 请求
          │
          ▼
       上游服务
```

Codex 和 xAI 不共用一套 OAuth 实现：

- Codex 使用非标准设备登录流程。
- xAI 使用 OIDC Device Authorization。

两者可以共用凭证来源接口，但 OAuth 请求、响应和错误处理分别实现。

## 包结构

```text
subscription/
  credential.go
  codex/
    oauth.go
  xai/
    oauth.go

providers/
  codex/
    codex.go
  xaioauth/
    xaioauth.go
```

不新增通用 OAuth framework、middleware pipeline 或持久化抽象。

## CredentialSource

`subscription` 包定义调用边界：

```go
package subscription

type Credential struct {
	AccessToken string
	AccountID   string // 仅 Codex 使用；xAI 留空
}

type CredentialSource interface {
	Credential(ctx context.Context) (Credential, error)

	// RefreshRejected 处理刚被上游以 HTTP 401 拒绝的 access token。
	// 实现必须先检查持久化状态中是否已经存在更新的 token，再决定是否刷新。
	RefreshRejected(
		ctx context.Context,
		rejectedAccessToken string,
	) (Credential, error)
}
```

接口约定：

1. `Credential` 返回当前可用凭证。调用方自行读取 store，并在需要时刷新和保存。
2. 实现必须支持并发调用。
3. `RefreshRejected` 不能盲目刷新。另一个请求可能已经刷新并保存了新 token。
4. 返回错误时不得包含 access token、refresh token 或 authorization code。
5. `uniai` 不缓存接口返回值，也不写回 token。

该接口不包含 headers。`Authorization` 和 `ChatGPT-Account-ID` 由 provider 根据结构化凭证生成，避免调用方直接控制认证 header。

## 无状态 OAuth API

### Codex

```go
package subscription/codex

func RequestDeviceCode(ctx context.Context, cfg OAuthConfig) (DeviceCode, error)
func PollDeviceCode(ctx context.Context, cfg OAuthConfig, code DeviceCode) (Token, error)
func RefreshToken(ctx context.Context, cfg OAuthConfig, refreshToken string) (Token, error)
```

Codex `Token` 保存协议返回的数据：

```go
type Token struct {
	IDToken      string
	AccessToken  string
	RefreshToken string
	AccountID    string
	PlanType     string
	ExpiresAt    time.Time
}
```

包内负责从 JWT claim 中解析 `AccountID`、`PlanType` 和过期时间，但不验证 JWT 签名。JWT 解析只用于读取上游已经签发的凭证元数据，不能用于身份认证判断。

### xAI

```go
package subscription/xai

func RequestDeviceCode(ctx context.Context, cfg OAuthConfig) (DeviceCode, error)
func PollDeviceCode(ctx context.Context, cfg OAuthConfig, code DeviceCode) (Token, error)
func RefreshToken(ctx context.Context, cfg OAuthConfig, refreshToken string) (Token, error)
func RevokeToken(ctx context.Context, cfg OAuthConfig, token Token) error
```

xAI `Token` 保存协议返回的数据：

```go
type Token struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	Scope        string
	ExpiresAt    time.Time
}
```

OAuth 函数只接收参数并返回结果：

- 不读写文件。
- 不使用进程级全局刷新锁。
- 不打开浏览器。
- 不等待下一次轮询时间。
- 不输出应用专用提示。

`OAuthConfig` 可以接受调用方提供的 `http.Client`。正式请求仍需校验 issuer 和 endpoint，避免 OAuth token 被发送到非预期主机。

## 根 Client 配置

`Config` 增加两个可选字段：

```go
type Config struct {
	// 现有字段省略。

	CodexSubscription subscription.CredentialSource
	XAISubscription   subscription.CredentialSource
	SubscriptionHTTPClient *http.Client
}
```

`SubscriptionHTTPClient` 只替换推理请求使用的 HTTP client，正式 URL 仍由 provider 固定。OAuth 函数使用各自 `OAuthConfig.HTTPClient`。这两个入口便于调用方配置代理、超时和测试 transport，不允许把 subscription token 发送到自定义 base URL。

### Codex 路由

`Provider: "openai_codex"` 保持现有 provider 名称：

- `CodexSubscription != nil`：使用 Codex subscription。
- `CodexSubscription == nil`：保持现有 API key 与自定义 endpoint 行为。

配置了 `CodexSubscription` 后，如果凭证不可用，必须直接返回认证错误，不能静默回退到 `OpenAIAPIKey`。同一个 `Client` 可以保留 `OpenAIAPIKey` 供其他 provider 使用。

### xAI 路由

- `Provider: "xai"`：保持现有 xAI API key 行为。
- `Provider: "xai_oauth"`：要求 `XAISubscription != nil`，使用 Grok Build subscription。

`xai_oauth` 不回退到 `xai` API key。

## Provider 请求流程

每次 subscription 请求执行：

1. 调用 `CredentialSource.Credential`。
2. 校验 access token 非空。
3. 生成 provider 自己控制的认证 headers。
4. 发送请求。
5. 非 HTTP 401 错误直接返回。
6. HTTP 401 时调用 `RefreshRejected`。
7. 使用返回的新凭证重试一次。
8. 第二次失败直接返回，不继续刷新或重试。

刷新和重试使用原始请求的 `context.Context`，不能脱离调用方的超时或取消控制。

## Codex 请求规则

Codex subscription provider 复用 `openai_resp` 的 Responses 请求、流式解析、tool calls、reasoning 和 usage 转换，并增加：

- 使用固定 Codex subscription API base。
- 将 system/developer 内容放入顶层 `instructions`。
- 设置 `store: false`。
- 保留现有 `openai_codex` 字段过滤规则。
- 从凭证生成 `ChatGPT-Account-ID`。
- 调用方 headers 不能覆盖 `Authorization` 或 `ChatGPT-Account-ID`。
- 上游请求始终使用 streaming，以兼容 Codex subscription endpoint；调用方未设置 `OnStream` 时，provider 在内部聚合 `response.completed`，仍返回普通 `chat.Result`。

不把订阅 access token 发送到调用方配置的自定义 endpoint。自定义 endpoint 继续使用现有 API key 路径。

## xAI 请求规则

`xai_oauth` 使用 xAI Responses API，并复用 `openai_resp` 的请求和响应转换：

- 使用固定 xAI subscription API base。
- 支持文本、图片输入、tool calls 和 streaming。
- 调用方 headers 不能覆盖 `Authorization`、`Proxy-Authorization`、`X-API-Key` 或 `API-Key`。
- HTTP 403 返回订阅资格错误。
- HTTP 429 保留安全的 retry-after 信息。
- HTTP 404 表示模型对当前账户不可用。

## Usage 与费用

Subscription 请求仍返回：

- input tokens
- cached input tokens
- output tokens（沿用上游定义，包含其计入 output 的 reasoning tokens）
- total tokens

但不按 API token 单价推导货币费用：

```go
result.Usage.Cost == nil
```

这一规则同时适用于 blocking 结果和最终 streaming event。

根 `Client` 的费用注入逻辑必须识别 subscription 请求并跳过，不能只在 provider 返回前清空 `Usage.Cost`。

## 调用方示例

调用方实现自己的 store 和凭证来源：

```go
type codexCredentialSource struct {
	store tokenStore
}

func (s *codexCredentialSource) Credential(
	ctx context.Context,
) (subscription.Credential, error) {
	token, err := s.store.Load(ctx)
	if err != nil {
		return subscription.Credential{}, err
	}

	if tokenNeedsRefresh(token) {
		token, err = codex.RefreshToken(ctx, oauthConfig, token.RefreshToken)
		if err != nil {
			return subscription.Credential{}, err
		}
		if err := s.store.Save(ctx, token); err != nil {
			return subscription.Credential{}, err
		}
	}

	return subscription.Credential{
		AccessToken: token.AccessToken,
		AccountID:   token.AccountID,
	}, nil
}
```

然后交给 `uniai`：

```go
client := uniai.New(uniai.Config{
	Provider:          "openai_codex",
	OpenAIModel:       model,
	CodexSubscription: source,
})
```

这里的 store、刷新锁和 `tokenNeedsRefresh` 都属于调用方代码。

## MisterMorph 迁移

迁入 `uniai`：

- `internal/codexauth/oauth.go` 中的无状态 OAuth 与 token 解析。
- `internal/xaiauth/oauth.go` 中的无状态 OAuth、OIDC discovery 与 token 解析。
- `providers/codex` 中的 Codex 请求适配。
- `providers/xaioauth` 中的 xAI subscription 请求适配和安全错误映射。

留在 MisterMorph：

- `internal/codexauth/store.go`。
- `internal/xaiauth/store.go`。
- token 文件路径、权限和原子写入。
- 刷新锁与 `CredentialSource` 实现。
- Cobra、Viper、登录输出和默认 provider 修改。
- daemon HTTP handler 与登录 session 管理。

MisterMorph 的 token JSON 格式应保持兼容，升级后不要求用户重新登录。

迁移完成后，MisterMorph 的专用 Codex/xAI provider 可以删除，普通 `llm.Request` 到 `uniai` 的适配层继续保留。

## 独立调用方示例

`cmd/subscriptionproxy` 展示调用方应当承担的状态职责。它不是根包的一部分，也不会被 `uniai.New` 自动启用。

子命令：

```text
subscriptionproxy login  --backend codex|grok --token-file TOKEN_FILE
subscriptionproxy status --backend codex|grok --token-file TOKEN_FILE
subscriptionproxy logout --backend codex|grok --token-file TOKEN_FILE
subscriptionproxy serve  --backend codex|grok --token-file TOKEN_FILE --model MODEL_ID
subscriptionproxy serve  --codex-token-file CODEX_TOKEN_FILE --grok-token-file GROK_TOKEN_FILE
```

示例程序负责：

- 要求调用方用 `--token-file` 明确指定 token 文件，不提供默认路径。
- 用同目录临时文件原子替换 token，文件权限为 `0600`。
- 用调用方自己的互斥锁合并并发刷新。
- 启动服务前读取或刷新 token。
- 运行时按 `--refresh-interval` 定期检查 token，并保存旋转后的 refresh token。
- 双后端模式同时维护两套凭据；`grok-` 模型走 xAI，其他非空模型走 Codex。
- 提供 `/v1/chat/completions`、`/v1/responses` 和 `/healthz`。
- 为两个 OpenAI 兼容模型接口提供普通响应和 SSE 流式响应。

示例 HTTP 服务没有客户端认证，因此默认只监听 `127.0.0.1`。它不应直接暴露到公网。

## 安全要求

1. OAuth 和 provider 错误不得包含 access token、refresh token、authorization code 或 device code。
2. debug 日志不得记录认证 headers。
3. subscription access token 只发送到固定官方 API base。
4. OAuth discovery 必须校验 issuer 与 endpoint host，并禁止不受信任的重定向。
5. 调用方 headers 不能覆盖 provider 生成的认证 headers。
6. `RefreshRejected` 返回的 access token 必须与被拒绝的 token 不同；否则不发送第二次请求。

## 测试

实现前先添加测试。

### Phase 1：无状态 OAuth

覆盖：

- Codex 设备码申请、轮询、token exchange 和刷新。
- xAI OIDC discovery、设备码申请、pending、slow-down、拒绝、过期、刷新和撤销。
- Account ID、plan type 和过期时间解析。
- 错误信息不泄漏 token。
- OAuth 函数不访问文件系统。

### Phase 2：CredentialSource 与 provider

使用 fake `CredentialSource` 覆盖：

- 每个请求获取一次凭证。
- HTTP 401 后刷新并重试一次。
- 非 401 不刷新。
- 第二次 401 不继续重试。
- 空 access token 直接报错。
- 恶意自定义 headers 被删除。
- Codex account ID 只能来自结构化凭证。
- blocking 和 streaming 的 `Usage.Cost` 都为空。

### Phase 3：独立调用方示例

覆盖：

- token 文件可以直接读取，写入权限为 `0600`。
- 并发请求只执行一次真实刷新。
- 刷新成功后保存新 token。
- `RefreshRejected` 先识别其他请求已经保存的新 token。
- 定时刷新循环随 context 停止。
- Chat Completions 与 Responses 的普通和流式 HTTP 响应。
- 双后端 HTTP 服务按请求模型选择 Codex 或 xAI。

验收命令：

```bash
go test ./...
```

MisterMorph 迁移时还应在其仓库运行完整测试。

## 非目标

- 不自动读取 Codex CLI 或 Grok CLI 的 token 文件。
- 不提供文件、数据库或 keychain store。
- 根包不提供登录 CLI、默认 token 路径或 Web handler；`cmd/subscriptionproxy` 仅作为独立调用方示例。
- 不提供 subscription 剩余额度查询。
- 不为订阅请求计算按 token 计价的费用。
- 不在认证包中维护默认模型。
- 不把 Codex 和 xAI 强行合并成同一套 OAuth 实现。

## 官方参考与稳定性

- OpenAI authentication：<https://learn.chatgpt.com/docs/auth>
- Codex App Server：<https://learn.chatgpt.com/docs/app-server>
- xAI Grok OAuth integration：<https://x.ai/news/grok-kilocode>

OpenAI 官方文档确认 ChatGPT 登录使用订阅额度，并将 App Server 作为带认证的产品嵌入接口。当前 Codex 直接 HTTP 登录和 inference endpoint 没有同等级别的公开稳定性承诺，因此相关常量和协议实现必须限制在 `subscription/codex` 与 `providers/codex` 内，不能泄漏到根 API。
