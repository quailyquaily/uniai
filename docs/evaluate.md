# Evaluate：结构化判断

`Client.Evaluate` 接收共享状态和具名问题，返回真假判断、选项或等级评分。原生路径支持 TypeSafe AI 的 Jev；Chat 模拟路径复用现有 Chat provider，包括使用兼容接口的自部署 Qwen。

批量检查延迟和判断结果可使用 [evalbench](../cmd/evalbench/README.md)：从环境变量读取连接配置，内置 18 个场景、360 条带预期答案的案例，支持 JSON 报告。

## 原生 Jev

```go
package main

import (
    "context"
    "log"
    "os"
    "time"

    "github.com/quailyquaily/uniai"
    "github.com/quailyquaily/uniai/evaluate"
)

func main() {
    client := uniai.New(uniai.Config{
        TypeSafeAPIKey: os.Getenv("TYPESAFE_API_KEY"),
        EvaluateModel: "jev-1.13.0",
    })
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    result, err := client.Evaluate(ctx, evaluate.Request{
        State: map[string]any{
            "message": "我重复付了两次款，请退回多付的一笔。",
        },
        Questions: map[string]evaluate.Question{
            "refund": {
                Kind: evaluate.Boolean,
                Instructions: "用户是否请求退款？",
            },
            "team": {
                Kind: evaluate.Choice,
                Instructions: "哪个团队应处理这个请求？",
                Options: map[string]any{
                    "billing": "支付、账单和退款",
                    "support": "产品使用和技术支持",
                },
            },
            "urgency": {
                Kind: evaluate.Score,
                Instructions: "按影响范围判断紧急程度。",
                Levels: []any{"一般咨询", "单个用户受影响", "服务大面积不可用"},
            },
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    log.Println("model:", result.Model, "emulated:", result.Emulated)
    if p := result.Answers["refund"].ProbabilityTrue; p != nil {
        log.Println("refund probability:", *p)
    }
    log.Println("team:", result.Answers["team"].Selected)
    log.Println("urgency:", *result.Answers["urgency"].ScoreValue)
}
```

TypeSafe 使用 `POST /v1/systemone` 和 Bearer API key。`TypeSafeAPIBase` 默认是 `https://api.typesafe.ai/v1`；客户端在 base 后追加 `/systemone`。可通过 `EvaluateHTTPClient` 指定原生请求的 HTTP client。[上游接口](https://docs.typesafe.ai/api)

## 用自部署 Qwen 模拟

同一份 `evaluate.Request` 可以交给 Chat 模拟。以下 `qwenAPIBase`、`qwenAPIKey`、`qwenModel` 和 `request` 均由调用方提供；需要导入 `chat` 包。

```go
effort := chat.ReasoningEffortLow
maxTokens := 2048

client := uniai.New(uniai.Config{
    OpenAIAPIBase: qwenAPIBase, // 例如 https://llm.example.com/v1
    OpenAIAPIKey: qwenAPIKey,
    EvaluateProvider: "openai",
    EvaluateModel: qwenModel,
    EvaluateEmulationMode: evaluate.EmulationForce,
    EvaluateEmulationOptions: &evaluate.EmulationOptions{
        ReasoningEffort: &effort,
        MaxTokens: &maxTokens,
    },
})

result, err := client.Evaluate(ctx, request)
if err != nil {
    return err
}
decision := *result.Answers["refund"].BooleanValue
```

这里使用已有 OpenAI 兼容 Chat 适配器，API key 仍须非空。无鉴权的自部署服务可使用服务允许的占位值。Chat 模拟使用所选 provider 的凭证、base 和 `ChatHeaders`；`EvaluateHTTPClient` 只影响原生 Evaluate。

模拟会把问题定义和派生的 JSON schema 放入 system 消息，把完整 State JSON 放入 user 消息。首版通过提示词要求结构化输出，并严格校验返回值；没有为各 provider 接入原生 JSON Schema constrained decoding，也不保证与 Jev 相同的判断质量。

### reasoning effort 与 token 上限

- `ReasoningEffort` 和 `MaxTokens` 通过既有 Chat 字段映射。请求中的非 nil 字段覆盖配置中的对应默认值；显式 `ReasoningEffortNone` 也会覆盖默认值。
- 自部署服务必须支持收到的参数。传入 `low`、`medium`、`high` 不保证服务实际区分这些档位；具体行为取决于模型、服务和 chat template。
- Schema 限定最终答案结构，不能推出 tokenizer 无关的精确 token 数，也不能推导 reasoning 的开销。`MaxTokens` 是否包含 reasoning 由后端决定；示例的 2048 只是配置值。
- 首版没有独立的 `thinking_token_budget` 或 `chat_template_kwargs` 透传字段。
- 已知会忽略参数的路径会提前返回 `ErrUnsupported`：`openai_codex` 的显式 MaxTokens，以及 `azure`、`cloudflare` 的显式 ReasoningEffort。其他模型限制沿用对应 Chat 适配器的检查和上游错误。

达到上限而被截断的响应会报错，不返回残缺答案，不追加纠正请求。

## 路由与默认值

| 参数 | 解析顺序 |
| --- | --- |
| Provider | `Request.Provider` → `Config.EvaluateProvider` → `typesafe` |
| Model | `Request.Model` → `Config.EvaluateModel`；仍为空则报错 |
| EmulationMode | 请求 → Evaluate 配置 → `off` |
| EmulationOptions | 模拟路径上按非 nil 字段合并请求与 Evaluate 配置 |

Evaluate 不继承 `Config.Provider` 或 Chat 模型默认值。

| 模式 | 行为 |
| --- | --- |
| `off` | 只调用原生 Evaluate；当前只有 `typesafe` |
| `fallback` | 发送前判断：有原生路径则用原生，否则使用同一 provider/model 的 Chat 路径 |
| `force` | 只使用选定 provider/model 的 Chat 路径 |

`fallback` 不接受第二套备用 provider/model，也不会在原生 HTTP、超时或解析失败后再尝试 Chat。`typesafe` 没有 Chat 路径，因此不能使用 `force`。模拟每次调用一次 `chatOnce`；底层 Chat SDK 自带的重试行为仍然适用。

原生路径忽略配置中的模拟参数，但拒绝请求中显式提供的 `EmulationOptions` 和仅用于 Chat 计价的 `InferenceProvider`。

## 问题与答案

| Kind | 问题字段 | 原生 Jev 答案 | Chat 模拟答案 |
| --- | --- | --- | --- |
| `Boolean` | Instructions；可选 TrueDescription / FalseDescription | ProbabilityTrue | BooleanValue |
| `Choice` | Instructions、非空 Options | Selected、Probabilities | Selected |
| `Score` | Instructions、至少两个有序 Levels | ScoreValue、小数评分、Probabilities | ScoreValue、整数等级索引 |

Score 使用从 0 开始的等级尺度。模拟要求整数形式的 JSON 数字，不接受小数或指数表示。公共 `ScoreValue` 为 `*float64`，可同时表达原生小数评分与模拟整数评分。Jev 的概率不会自动按 0.5 转成 bool；业务阈值由调用方决定。

State、Instructions 和各描述支持字符串、对象、数组，嵌套值支持 JSON 原始类型。State 不允许 null，但可为空字符串、对象或数组。Instructions 必须存在，字符串不能只有空白。Choice 描述允许 null，Score 等级描述不允许 null。不同 Kind 的字段不能混用。TypeSafe 限制 Choice 最多 255 项、Score 最多 10 级；这些上限不应用于 Chat 模拟。[上游问题类型](https://docs.typesafe.ai/primitives)

答案必须覆盖全部问题。模拟仅接受完整 JSON 对象或单一 JSON 代码块；额外字段、重复字段、缺失字段、null、非法选项、越界等级、夹带正文、意外工具调用和未正常结束的生成都会返回错误。

`BooleanValue`、`ProbabilityTrue`、`ScoreValue` 和 token 计数用指针区分缺失与 false/0。模拟不编造概率分布或 confidence。TypeSafe 的 confidence 和 legend 保存在 `ProviderMetadata["typesafe"]` 中。

`Result.Model` 保留底层适配器返回的模型标识，可能仍是别名。`Raw` 保存原生响应或模拟的答案 JSON，默认 JSON 序列化时排除。请求中的 map、slice 和指针值在调用期间不能被并发修改。

## 用量与费用

原生 usage 保留缺失计数；仅在 input 和 output 都存在时补算缺失的 total。原生费用使用 `PricingCatalog.Evaluate`，按 provider 和响应模型名严格匹配，不使用 Chat 价格，也不跨 provider 或按任意 `/` 后缀猜测价格。

内置 Jev 价格与显式别名见 [`pricing.example.yaml`](../pricing.example.yaml)。未知模型或缺少 input/output 时，Cost 为 nil；完整的零用量和零价输出有效。

模拟沿用实际 Chat 请求的费用、缓存计费和 `InferenceProvider` 提示。完整 Chat usage 和 warnings 保存在 `ProviderMetadata[provider]` 中。当前 Chat usage 使用 int，因此模拟结果无法恢复上游计数“缺失”与“明确为零”的区别。传入空 `PricingCatalog` 可禁用估算。[费用配置](pricing.md)

## 错误与接入边界

用 `errors.Is` 检查 `evaluate.ErrInvalidRequest`、`evaluate.ErrUnsupported`、`evaluate.ErrInvalidResponse`。原生非 2xx 响应使用 `*evaluate.APIError`，可通过 `errors.As` 取得 StatusCode、RetryAfter 和 Body。Error 文本不包含响应 Body。网络错误保留原始 cause，支持检查 context 取消或超时。

原生适配器不自动重试，复用已有响应体大小限制并关闭 body。Chat 模拟不执行工具、不启用流式回调、不请求模型修复输出。

首版没有通用 `systemone` 兼容入口。Laya、Kev、Nimble 等原生服务仍需要适配和语义验证；可通过普通 Chat 接口运行的模型可使用上述模拟路径。详见[设计方案](feat/feat_20260921_evaluate_api.md)。
