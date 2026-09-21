# Evaluate API、Jev 接入与 Chat 模拟方案（2026-09-21）

## 状态与目标

状态：首版已实现。公共类型、TypeSafe 原生适配、Chat 模拟、根 Client 路由及费用估算均已完成；调用方式见 [Evaluate 使用文档](../evaluate.md)。自动化验证使用离线 HTTP 替身，尚未进行真实服务联调。

实现中补齐了模拟所依赖的 Chat 响应状态：Bedrock 和 Cloudflare 保留生成结束原因，OpenAI 兼容转换保留拒答标记，避免 Evaluate 把未完成或拒答响应当成成功。模拟 Score 先按 Go 整数解析，再转成公共浮点评分，不接受小数或指数表示，防止浮点舍入把小数误当成整数等级。

验证记录（2026-09-21）：`go test ./...`、`go vet ./...`、`git diff --check` 均通过；使用文档的完整 Go 示例编译通过。测试覆盖公共校验、原生 HTTP 映射、模拟参数传递与严格解析、费用隔离，以及生成结束状态和拒答标记的传递。

为 uniai 增加 `Client.Evaluate`，接收共享状态和一组具名问题，返回程序可直接使用的结构化答案。支持原生判断接口和通过普通 Chat 模拟判断两种执行方式。首个原生适配器为 TypeSafe AI 的 Jev；模拟路径复用现有 Chat provider。

本方案交付公共类型、TypeSafe 适配器、Chat 模拟、根 Client 路由、usage 和费用处理，以及对应测试。模拟采用与 tool calling 模拟一致的 `off / fallback / force` 命名，并单独定义触发条件。业务阈值、人工复核、后续动作和模型选择策略由调用方负责。

## 现有结构与接入位置

- [`chat`](../../chat/types.go) 的请求围绕消息、生成参数和工具调用组织。Evaluate 使用独立的业务请求类型；模拟时将它转换成 Chat 请求。
- [`classify`](../../classify/types.go) 使用 `Input + Labels`，结果沿用 Jina 的分类结构，不能完整表达真假概率和有序等级评分。
- [`client.go`](../../client.go) 已负责 chat provider 路由；Evaluate 采用同样的直接路由方式。
- [`tool_emulation.go`](../../tool_emulation.go) 已采用“生成指令、调用 Chat、解析结果”的实现方式；Evaluate 复用 Chat 调用路径，不套用工具决策流程。[现有工具模拟行为](../tool_emulation.md)
- [`internal/httputil`](../../internal/httputil/httputil.go) 已提供 context 超时处理和响应体大小限制；新适配器复用这些行为。
- [`pricing.go`](../../pricing.go) 使用独立的 evaluate 价格组估算原生费用；模拟沿用实际 Chat 调用的费用处理。

`Chat`、`Classify` 及其现有配置和行为保持兼容。Evaluate 单独选择 provider/model，不继承聊天默认选择。选定 Chat 模拟后，使用该 provider 已有的凭证和连接配置。

## 首个上游与公共语义

TypeSafe 使用 `POST https://api.typesafe.ai/v1/systemone`，Bearer API key 鉴权。请求由 `model`、`state` 和 `questions` 组成，响应按问题 ID 返回答案。它的三种原生问题为 Noul、Choice 和 Score。[API 文档](https://docs.typesafe.ai/api)

公共层保留三个有明确边界的类型：

| 类型 | 问题含义 | 必需答案 | 可选信息 |
| --- | --- | --- | --- |
| `Boolean` | 一个命题是否成立 | 布尔判断或成立概率，至少一项 | 两项都由上游提供时均保留 |
| `Choice` | 从给定选项中选择一个 | 一个有效的选项 ID | 各选项的概率分布 |
| `Score` | 按给定的有序等级评分 | 等级尺度上的浮点位置 | 各等级的概率分布 |

`Boolean` 是问题类别，不保证上游返回 Go `bool`。Jev 的 Noul 只映射为成立概率，不自动使用 `0.5` 或其他阈值生成布尔值。

Score 的等级按请求中的顺序编号为 `0..N-1`，结果范围相同。例如三个等级为“低、中、高”，`1.4` 表示该尺度上的位置。公共层不要求所有模型都用概率期望计算评分，也不从分布重新计算或覆盖上游评分。TypeSafe 的评分计算方法属于其适配语义。[问题类型说明](https://docs.typesafe.ai/primitives)

Chat 模拟首版返回布尔判断、选项 ID 和整数等级索引。它不提供成立概率、候选概率分布或统一 confidence。模拟的整数评分符合公共等级尺度，但不等于 Jev 的概率加权评分。调用方可以通过 `Result.Emulated` 区分执行方式。

原始 reward、logit、任意数值预测、排序和多选不属于这三个类型。未来模型若不能保持上述语义，应增加有明确契约的新类型或使用其他能力接口，不能通过改名强行映射。

## 公共请求

`evaluate` 包只定义类型和公共校验，不依赖具体 provider 或根包。

```go
package evaluate

type Kind string

const (
    Boolean Kind = "boolean"
    Choice  Kind = "choice"
    Score   Kind = "score"
)

type EmulationMode string

const (
    EmulationOff      EmulationMode = "off"
    EmulationFallback EmulationMode = "fallback"
    EmulationForce    EmulationMode = "force"
)

type Request struct {
    Provider          string
    Model             string
    EmulationMode     EmulationMode
    EmulationOptions  *EmulationOptions
    InferenceProvider string // 模拟时透传给 Chat，供网关等场景计价。
    State             any
    Questions         map[string]Question
}

type EmulationOptions struct {
    ReasoningEffort *chat.ReasoningEffort
    MaxTokens       *int
}

type Question struct {
    Kind         Kind
    Instructions any

    // Choice：选项 ID 到描述；nil 描述表示没有补充说明。
    Options map[string]any

    // Score：从低到高排列的等级描述。
    Levels []any

    // Boolean：可选的真假含义说明。
    TrueDescription  any
    FalseDescription any
}
```

使用普通结构体和枚举，不为每种问题建立接口、实现类或自定义 builder。公共 `Question` 不直接序列化成上游请求；`Kind` 和 `Options` 等字段由适配器转换成对应协议。

`Questions` 就是调用方提供的判断 schema：map 的键定义输出字段，`Kind` 定义答案类型，`Instructions` 定义判断含义，`Options` 或 `Levels` 定义答案范围及其说明。不另设一份 `Schema`，也不承诺支持任意嵌套 JSON Schema。适配器将这份定义转换为原生协议或模拟指令，调用方无需维护两份问题定义。

`EmulationOptions` 只暴露当前需要的推理强度和生成上限，类型复用 Chat 的定义。它不接收完整 `chat.Request` 或 `chat.Options`，避免调用方覆盖 SDK 生成的消息、输出定义和工具/流式设置。

`State`、`Instructions` 和描述字段中的 `any` 只用于承载 JSON 内容，不用于保存任意运行时对象：

- 序列化后的根值必须是字符串、对象或数组；对象内部可包含合法 JSON 标量。
- 支持可编码的 Go struct、map、slice 和合法的 `json.RawMessage`。
- `State` 不接受缺失值或 JSON null；空字符串、空对象和空数组保持原样。
- `Instructions` 必须提供；字符串形式不能只有空白。
- Choice 描述可以为 null；Score 等级描述不能为空值；真假说明未设置时省略。
- 循环引用、函数、NaN 等不能编码为 JSON 的值在发请求前报错。
- 原生适配器保留 JSON 值的形状；模拟路径将完整 JSON 内容编码进 Chat 消息。两条路径都不裁剪正文或截断列表。调用期间只读请求，调用方不得并发修改其中的 map 或 slice。

公共校验覆盖：provider/model 解析后的非空要求、已知 EmulationMode、已设置的 MaxTokens 大于零、非空问题集合、非空问题 ID、已知 Kind、内容可编码性，以及各类型字段的互斥关系。Choice 的 Options 必须非空，且所有选项 ID 非空；Score 至少有两个等级。字段属于其他问题类型时返回错误，不静默忽略。

各供应商的数量上限、支持的内容形式和附加限制由适配器校验。TypeSafe 的选项上限不成为整个 `evaluate` 包的上限。

## 公共结果

以下省略 JSON tag；实现使用指针字段和 `omitempty` 保留“缺失”与零值的区别。

```go
type Answer struct {
    Kind Kind

    // Boolean：至少一项存在；两项之间不隐含固定阈值关系。
    BooleanValue    *bool
    ProbabilityTrue *float64

    // Choice：必须属于请求的 Options，选项 ID 不允许为空。
    Selected string

    // Score：0..len(Levels)-1，可以是小数。
    ScoreValue *float64

    // Choice 使用选项 ID；Score 使用等级索引的十进制字符串。
    // nil 表示上游没有提供分布。
    Probabilities map[string]float64
}

type Result struct {
    Provider string
    Model    string
    Emulated bool
    Answers  map[string]Answer
    Usage    *Usage

    ProviderMetadata map[string]json.RawMessage
    Raw              json.RawMessage // json:"-"
}

type Usage struct {
    InputTokens  *int
    OutputTokens *int
    TotalTokens  *int
    Cost         *chat.UsageCost
}
```

`Usage.Cost` 复用当前 image 使用的 `chat.UsageCost`。`EmulationOptions.ReasoningEffort` 复用 `chat.ReasoningEffort`；本次不迁移这些已有公共类型，也不建立第二套推理强度枚举。

结果约定：

1. 成功结果必须覆盖请求中的全部问题，答案 Kind 必须匹配。缺少答案、出现未知问题 ID、答案类型错误或必需值缺失，均返回响应错误。
2. `false`、概率 `0` 和评分 `0` 都是有效值，不能按缺失处理。答案中不应出现其他 Kind 的值字段。
3. 概率必须是有限数且在 `[0,1]` 内；评分必须是有限数且在请求定义的等级范围内。分布一旦提供，键必须完整对应选项或等级。缺少分布不能用空分布或 one-hot 分布补齐。
4. 上游可能对数值做舍入。适配器按已确认的精度校验分布总和；公共层不硬编码所有供应商通用的容差，也不归一化返回数值。
5. `Provider` 保存实际调用的 provider；`Model` 保存上游报告的模型标识，未报告时留空。该标识可能只是回显的别名，不保证是已解析的权重版本；SDK 不自行把请求模型填成响应模型。
6. 原生路径未提供 usage 时为 nil；单个 token 计数缺失时也保留 nil。只有 input/output 都存在时，才可补算未报告的 total。所有计数必须非负；模拟路径沿用 Chat 已归一化的计数，具体限制见费用一节。
7. 原生路径的 `Raw` 保存 HTTP 响应体；模拟路径保存校验通过的答案 JSON。普通 JSON 序列化不包含它。结构化调用方使用 `Answers`，不依赖 Raw 的稳定性。
8. `Emulated` 在原生路径为 false，在 Chat 模拟路径为 true；它记录实际执行方式，不直接复制请求中的模式。

Jev 的 `confidence` 是从概率分布计算的统计量，不能作为所有模型统一的“正确率”。它和 Score 的原生 `legend` 放在 `ProviderMetadata["typesafe"]` 中，按问题 ID 保存。[置信度文档](https://docs.typesafe.ai/confidence)

该元数据的首版约定为：

```json
{
  "answers": {
    "department": { "confidence": 0.81 },
    "severity": {
      "confidence": 0.92,
      "legend": { "0": "低", "1": "中", "2": "高" }
    }
  }
}
```

新 provider 使用自己的命名空间。不支持的字段保持缺失；不制造统一的 confidence 计算公式。

## 配置、入口与包依赖

根 `Config` 增加：

```go
EvaluateProvider         string
EvaluateModel            string
EvaluateEmulationMode    evaluate.EmulationMode
EvaluateEmulationOptions *evaluate.EmulationOptions
EvaluateHTTPClient       *http.Client // 仅用于原生 Evaluate HTTP 调用。

TypeSafeAPIKey  string
TypeSafeAPIBase string
```

解析规则：

- provider：`Request.Provider` → `Config.EvaluateProvider` → `"typesafe"`。
- model：`Request.Model` → `Config.EvaluateModel` → 缺失错误。
- mode：`Request.EmulationMode` → `Config.EvaluateEmulationMode` → `evaluate.EmulationOff`。空值表示继承，显式 `off` 可以覆盖配置中的 `force`。
- 模拟参数按字段解析：请求中的非 nil 值 → 配置中的非 nil 值 → 不发送该参数。请求只设置 MaxTokens 时仍可继承配置的 ReasoningEffort；显式 `ReasoningEffortNone` 表示请求关闭思考，不等同于未设置。
- TypeSafe base：配置值 → `https://api.typesafe.ai/v1`；适配器追加 `/systemone`。
- API key 只读显式配置。SDK 不读取环境变量或自动申请凭证。
- 不使用 `Config.Provider`、`OpenAIModel` 等聊天默认值补全 Evaluate 的 provider/model。
- 原生路径使用对应 Evaluate 适配器的配置。模拟路径使用选定 Chat provider 的现有凭证、base、`ChatHeaders` 和 HTTP 行为；不使用 TypeSafe 凭证或 `EvaluateHTTPClient` 替代它们。

要求显式指定模型，便于调用方选择固定版本或滚动别名。当前文档列出的固定版本为 `jev-1.13.0`；别名和价格可能变化，实际实现时需重新核对。[模型文档](https://docs.typesafe.ai/models)

根入口与 provider 方法分别为：

```go
// package uniai
func (c *Client) Evaluate(ctx context.Context, req evaluate.Request) (*evaluate.Result, error)

// package providers/typesafe
func (p *Provider) Evaluate(ctx context.Context, req *evaluate.Request) (*evaluate.Result, error)
```

根方法解析一份请求副本，在发送请求前确定原生或模拟路径，按 provider 做直接 switch。原生适配器允许单独使用，并负责完整的请求、响应校验；直接使用时只返回上游 usage，不读取根 PricingCatalog。Chat 模拟的编排放在根包，以复用 `Client.chatOnce` 和现有费用处理。

`typesafe.Config` 只包含 `APIKey`、`BaseURL`、`HTTPClient` 和 `Debug`；`New` 检查必要配置。直接调用适配器仍要求非空模型；Provider 为空时在请求副本中设为 `typesafe`，非空且不匹配时返回 Unsupported。根入口和独立入口执行相同的公共校验，避免只有根入口能拦截无效请求。

独立 TypeSafe 适配器只接受空模式或 `off`；其他模式返回 Unsupported。根入口选定原生路径后，将请求副本中的模式设为 `off` 再交给适配器。`InferenceProvider` 只用于模拟路径，原生路径收到非空值时返回 Unsupported，避免静默忽略配置。

请求中显式提供 EmulationOptions 而最终选择原生路径时返回 Unsupported，不因此触发模拟。配置中的 EvaluateEmulationOptions 只在模拟路径生效，允许同一个 Client 同时调用原生模型和模拟模型。解析参数时复制值，不修改配置或调用方的指针。

```text
uniai.Client.Evaluate
  ├─ 解析 provider/model/mode，执行公共校验
  ├─ 原生：providers/typesafe.Evaluate → 原生 Evaluation 计价
  └─ 模拟：问题转指令和 schema
       → Client.chatOnce → 严格解析和结果校验
       → 沿用 Chat 费用，标记 Emulated
```

首版无需 `evaluate.Client`、provider registry 或只有一个实现的接口。新增原生 provider 时增加适配器和路由；Chat 模拟共用一份实现，不为每个聊天供应商再写一个 Evaluate 适配器，也不把 `emulated` 注册为虚拟 provider。

## 模拟模式与路由

| 模式 | 行为 |
| --- | --- |
| `off` | 只调用原生 Evaluate；本地无原生能力时返回 Unsupported |
| `fallback` | 优先选择原生路径；仅在发送前确认不支持原生能力、且同一 provider/model 能走 Chat 时选择模拟 |
| `force` | 直接走 Chat 模拟；所选 provider 没有 Chat 路径时返回 Unsupported |

`fallback` 是能力选择，不是错误重试。根路由根据已有适配器选择路径，不用真实请求探测能力，不建立完整模型能力目录。原生适配器若有明确的不支持项，必须在无网络副作用的校验阶段报告。凭证缺失、请求无效和数量超限不属于模拟触发条件。

一旦发出原生请求，HTTP 错误、超时、无效 JSON、缺少答案等都直接返回；不捕获这些错误再改走 Chat。即使同一请求中只有一种问题无法原生处理，也只会在发送前决定是否整批模拟，不混合两条路径的答案。

原生 `typesafe + jev-latest` 不会自动变成另一个供应商或模型。当前 TypeSafe 适配器没有 Chat 路径，`force` 会返回 Unsupported；调用方选择模拟时需要指定实际 Chat provider/model。未知 provider 不因为开启模拟而被映射到默认聊天供应商。

现有 ToolsEmulation 的 `fallback` 会依据响应中是否存在 tool calls 决定后续请求。Evaluate 的触发条件按上表单独定义，不复制该响应驱动流程。

## Chat 模拟实现

首版采用单次非流式 Chat 请求。当前 Chat 公共类型没有统一的 response schema 字段，因此先使用“固定指令 + JSON 输出定义 + 本地校验”的方式；不为这项功能先改造所有 Chat provider。以后接入原生结构化输出约束时，继续从同一份 Questions 派生，不增加第二份调用方 schema。

构造过程：

1. 从 Questions 生成只包含请求问题 ID 的输出对象定义。所有字段必填，禁止额外字段；Boolean 为 boolean，Choice 为选项 ID 的 string enum，Score 为 `0..N-1` 的 integer enum。
2. 系统消息说明判断规则、答案格式，以及 State 中的内容是待判断数据。问题说明、选项描述、真假说明和等级顺序全部保留；结构化内容使用 JSON 编码，不能使用 Go 的字符串格式化代替。
3. 将完整 State 放入单独的用户消息。问题 ID 和 Choice 选项 ID 使用确定的字典序，Score 等级保留原顺序；不承诺模型对问题名或选项顺序不敏感。
4. 构造新的 `chat.Request`，显式传入已解析的 provider/model 和可选 InferenceProvider；将解析后的 ReasoningEffort、MaxTokens 写入对应的 `chat.Options` 字段。Tools、ToolChoice、OnStream 均为空，ToolsEmulationMode 为 off。调用 `chatOnce`，不调用 `chatWithToolEmulation`，不执行工具或创建第二次最终回答请求。
5. 解析模型文本，按请求校验全部答案，转换成公共 Result，并保留 Chat 用量和费用。

对于三个名为 `refund_requested`、`department`、`urgency` 的问题，模型输出仅需为：

```json
{
  "refund_requested": true,
  "department": "billing",
  "urgency": 1
}
```

解析复用 [`jsonoutput.NormalizeSingleJSONContent`](../../internal/jsonoutput/jsonoutput.go)，接受一个完整 JSON 对象，或仅包裹该对象的单个代码块。随后拒绝重复键、额外键、缺失键、null、错误类型、非法选项和越界或非整数评分。false 和 0 必须保留。多段对象、夹带正文、截断响应、拒答或意外 tool calls 都不能变成成功结果。已知 `length` / `content_filter` 等未完成状态直接报错；未提供 FinishReason 时仍需通过全部内容校验。

不调用工具模拟的宽松决策解析器，不修补残缺 JSON，不把无效输出视为“无答案”，也不自动发起纠正请求。模拟的 ProbabilityTrue 和 Probabilities 始终为 nil；即使模型额外输出自报概率，也按额外字段报错，不构造 one-hot 分布或 confidence。

一次生成中的后续答案可能受先前输出影响，不能承诺 Jev 的独立求值行为。SDK 保证每个问题的任务定义都以同一 State 为依据；若业务要求独立上下文，应由调用方分别请求。格式校验只能验证结构和答案范围，不能保证判断正确或任意模型都遵守格式。

模拟标记使用 `Result.Emulated`，不依赖警告字符串。Chat 的完整 usage 和已有 warnings 放入 `ProviderMetadata[provider]` 的 `chat_usage`、`chat_warnings`，用于保留缓存计数等未进入公共类型的信息；这些字段不会被解释为 Jev 的元数据。

### 自部署 Qwen、推理强度与 token 上限

自部署服务若提供兼容的 Chat Completions 接口，可通过 `Provider: "openai"` 配合显式 `OpenAIAPIBase` 和服务公布的模型名称调用。Evaluate 不根据 Qwen 名称猜测部署框架、chat template 或推理参数含义。

三个控制项分别处理：

| 控制项 | 作用 | 约束 |
| --- | --- | --- |
| `ReasoningEffort` | 请求某个推理强度档位 | 复用 Chat 映射；不保证每个服务都支持或区分相同档位，也不是 token 硬上限 |
| 后端 thinking budget | 限制思考阶段的 token 数量 | 属于具体推理服务能力，需要确认参数和版本，不能由 effort 自动推算 |
| `MaxTokens` | 限制该 Chat 路径的生成 token 数量 | 计数范围遵循所选服务；不能统一解释为“最终 JSON 的 token 数量” |

当前 [`providers/openai`](../../providers/openai/openai.go) 能把公共 ReasoningEffort 和 MaxTokens 转成请求字段。对普通 Qwen 模型名，MaxTokens 走 `max_tokens`。字段被 SDK 发出不代表服务一定按预期执行；已知不支持时在发送前报错，服务拒绝参数时保留错误，不静默删除参数或在提示词中假装实现同等控制。

Evaluate 还需检查所选 Chat 路径是否会忽略调用方显式设置的控制参数。例如当前 `openai_codex` 路径会移除输出上限，模拟请求若设置 MaxTokens，应在发送前返回 Unsupported。不能把已有 Chat 路径的参数忽略行为解释为成功应用了限额；这类检查只覆盖已知行为，不根据自部署模型名推断服务能力。

Qwen 的推理控制还取决于模型模板和服务版本。以当前 vLLM 文档为例，`reasoning_effort` 的 low/medium/high 可触发 `enable_thinking=true`，none 可触发 false；这项映射本身不保证三个档位拥有不同预算。需要独立思考上限时，vLLM 提供 `thinking_token_budget`。开启思考时，总生成上限需要给思考、结束标记和最终答案留出空间。[vLLM 推理控制](https://docs.vllm.ai/en/latest/features/reasoning_outputs/)

当前 OpenAI 兼容适配器明确拒绝公共 ReasoningBudget；[`oaicompat.ApplyOptions`](../../internal/oaicompat/convert.go) 也不会透传任意未知字段。因此不能把 `thinking_token_budget` 或 `chat_template_kwargs` 填进现有 OpenAI options 就宣称生效。具体部署需要这些扩展字段时，应先为 Chat 增加经过测试的最小映射，再由 Evaluate 复用；本次不增加未经服务验证的通用 effort-to-budget 换算。

Questions 的字段数、选项和等级已知，所以可以估算最终答案的预算。估算仍需考虑字段名、最长选项 ID、JSON 转义、格式空白和目标 tokenizer。schema 定义本身不限制推理长度；当前模拟路径把 schema 放入指令，也不等同于服务端约束解码。vLLM 等支持原生 JSON Schema 的服务可以据此约束答案生成，但仍需预算和完整性检查。[vLLM 结构化输出](https://docs.vllm.ai/en/latest/features/structured_outputs/)

MaxTokens 可以独立于 schema 设置，不自动从 schema 推导一个保证完成的精确值。未设置时保留服务默认值；设置后不自动增大。对于把思考计入总生成量的服务，应按“思考预算 + 答案预算 + 格式与终止余量”分配总上限。若不需要思考，可在已确认支持的服务上显式关闭，再使用较小的生成预算。限额过小导致截断时返回 InvalidResponse，不把半个 JSON 当成有效结果。

后端必须把思考内容与最终答案正确分离；Evaluate 只解析最终文本，不从混合的 `<think>` 正文中猜测答案。隐藏 reasoning 字段只改变响应内容，不代表停止思考或减少生成预算消耗。以上部署行为需要单独联调确认，离线请求映射测试只能证明发送了什么参数。

## TypeSafe 适配规则

| 公共字段 | TypeSafe 请求或响应 |
| --- | --- |
| `State`、`Model` | `state`、`model`，内容原样保留 |
| 问题 map 的键 | `questions` / `answers` 的键，原样保留 |
| `Kind: Boolean` | 请求 `type: "noul"`；响应 `noul` → `ProbabilityTrue` |
| `TrueDescription` / `FalseDescription` | 可选的 `criteria.true` / `criteria.false` |
| `Kind: Choice`、`Options` | 请求 `type: "choice"`、`criteria`；响应 `choice` → `Selected` |
| `Kind: Score`、`Levels` | 请求 `type: "score"`、有序 `criteria`；响应 `score` → `ScoreValue` |
| 原生 `probabilities` | `Probabilities`，保留上游数值和键 |
| 原生 `confidence`、`legend` | TypeSafe 元数据，按问题 ID 保存 |

首版必须按 TypeSafe 的协议要求检查必需字段。公共层允许其他模型不返回分布，不代表 Jev 缺失其必需分布时仍可当作正常响应。

TypeSafe 当前 Choice 最多 255 个选项，Score 接受 2–10 个等级；这些检查放在适配器中。请求状态、问题说明和描述支持的 JSON 形式以其协议为准。[API 文档](https://docs.typesafe.ai/api)

数值精度处理需要覆盖公开 SDK 已说明的舍入情况。TypeSafe 的 AI SDK 适配声明两位小数精度；对于包含 K 个概率的分布，舍入造成的总和偏差上界为 `K × 0.005`，另加浮点运算误差。只有确认上游使用该精度的适配路径采用这个容差，保持原值返回。实现时重新核对官方 SDK 的当前说明。[TypeSafe AI SDK 适配说明](https://www.npmjs.com/package/@ai-sdk/typesafe-ai)

所有独立问题随共享状态在一次请求中发送。SDK 不拆分超限请求，不把前一个答案自动注入后一个问题；存在答案依赖时，由调用方构造下一次请求。

## 错误、取消和请求生命周期

首版每次 Evaluate 在发送前选定一条路径，只发起一次原生 Evaluate 调用或一次 `chatOnce` 调用。Evaluate 层不增加重试、格式修复请求、模型切换或失败后的模拟。Chat provider 已有的底层 SDK 重试和凭证处理保持原行为，因此这里的“一次”指逻辑调用，不承诺只有一个 HTTP 网络请求。

- 公共请求错误使用可供 `errors.Is` 判断的 `evaluate.ErrInvalidRequest`，错误消息标明问题 ID 和字段路径。
- 不支持的 provider、已知但不支持的问题类型或可选能力使用 `evaluate.ErrUnsupported`，在发送前返回；模式允许且存在同 provider/model 的模拟路径时，由本地路由选择模拟。未知 Kind 或 EmulationMode 本身属于 InvalidRequest。
- 上游答案缺失、JSON 无效或语义不符使用 `evaluate.ErrInvalidResponse`；不返回看似成功的部分结果。
- 原生适配器的非成功 HTTP 状态使用 `evaluate.APIError`，保留 `Provider`、`StatusCode`、`Body` 和原始 `RetryAfter` 字符串，供 `errors.As` 读取。`Error()` 只输出 provider 和状态码，正文由调用方显式读取。模拟路径保留 Chat 的错误类型和 cause，不根据错误字符串猜测状态码。
- 网络错误和 context 错误保留 cause；取消和 deadline 能通过 `errors.Is` 识别。

TypeSafe 文档列出 `401`、`422`、`429` 和 `529` 等状态。调用方可结合状态码、`Retry-After` 和自己的重试预算处理限流或过载；SDK 不把普通 HTTP 422 误报成自己已完成的本地校验。[错误说明](https://docs.typesafe.ai/api#errors)

原生路径优先使用 `EvaluateHTTPClient`；未配置时使用 `httputil.ClientForContext(ctx)`。始终使用 `http.NewRequestWithContext`，响应体通过 `httputil.ReadBody` 读取并关闭。自定义 client 的超时设置不被 SDK 改写。模拟路径将同一 ctx 传给 Chat，并遵守其现有传输配置。

沿用 `Config.Debug` 和现有诊断函数，原生标签为 `typesafe.evaluate.request` / `typesafe.evaluate.response`，模拟转换标签为 `evaluate.emulation.request` / `evaluate.emulation.response`。默认不记录正文；启用 Debug 时正文可能包含业务状态。日志与错误不包含认证 header 或 API key。

一次请求只要有一个答案不合法，就返回 nil result 和错误。业务不确定性仍是成功答案，例如 `ProbabilityTrue = 0.5`，不触发重试。所有失败路径都不执行调用方业务动作。

## Usage 与费用

原生路径的 `PricingCatalog` 增加 `Evaluate []EvaluationPricingRule`，YAML 键为 `evaluate`。规则只包含当前确实需要的字段：

```go
type EvaluationPricingRule struct {
    InferenceProvider   string
    Model               string
    Aliases             []string
    InputUSDPerMillion  float64
    OutputUSDPerMillion float64
}
```

新增 `EstimateEvaluateCost(provider, model string, usage evaluate.Usage)`，返回 `(*chat.UsageCost, bool)`。原生费用复用现有模型名称规范化、token 费用计算和 catalog clone/校验惯例，不走 `EstimateChatCost`。

匹配规则：

- 必须匹配实际调用的 provider 和上游报告的模型标识，模型只做规范化后的精确匹配或显式别名匹配。
- 价格规则的 `InferenceProvider` 必填，取实际原生 provider；它不读取只用于 Chat 的 `Request.InferenceProvider`。同一 provider 内重复模型或别名是配置错误。
- 不跨 provider 回退，也不通过截取模型名的路径后缀猜测价格。网关与直连需要各自的价格记录。
- 响应模型标识缺失、价格不匹配、usage 不完整时不估算，`Cost` 保持 nil。
- InputTokens 与 OutputTokens 都存在才计算；免费输出不等于零 output tokens。
- 价格可为零但必须有限且非负。金额标为 `Estimated: true`。

按 2026-09-21 的官方文档，Jev 当前输入价为每百万 token 0.042 美元，输出免费。实现时增加固定版本及经核实的别名，并记录核对日期；后续未知版本不自动套用旧价格。[定价来源](https://docs.typesafe.ai/models)

旧配置未包含 `evaluate` 时仍可解析。自定义 catalog 完整覆盖默认 catalog，空 catalog 继续表示不做本地费用估算。更新 Clone 和 Validate 时不能改变现有 Chat/Image 的匹配行为。

模拟路径单独遵循现有 Chat 计价：

- 对真实 Chat 请求和响应调用 `annotateChatResultCost`，保留已计算的 Cost；请求中的 InferenceProvider 参与现有网关价格匹配。包括缓存价格、长上下文档位和请求模型回退在内的行为均沿用 Chat。
- 将结果中的 Chat Cost 复制到 Evaluate Usage，不再调用 `EstimateEvaluateCost`。不套用 Jev 价格，也不重复估算或相加。
- Chat usage 的 token 字段当前是整数，不能区分缺失与零。模拟结果保留这些已归一化计数，包括零值，并在 provider metadata 中保留完整 `chat_usage`；不宣称它们都是上游明确报告的原始计数。原生路径继续使用指针表示缺失。
- 一次模拟包含全部问题和输出定义的实际 token 消耗。解析失败仍返回错误，不伪造成功 usage；它不代表已发送的 Chat 请求没有产生费用。
- 不为“模拟”单独建立价格模型。不匹配价格时，保留 Chat 的 Cost 缺失状态。

## 调用示例

以下省略 imports，展示拟议的完整调用方式。问题使用结构体直接构造，不额外提供只替调用方填写字段的包装函数。

```go
client := uniai.New(uniai.Config{
    TypeSafeAPIKey: apiKey,
    EvaluateModel: "jev-1.13.0",
})

request := evaluate.Request{
    State: map[string]any{"message": "付款重复扣款，需要退款。"},
    Questions: map[string]evaluate.Question{
        "refund_requested": {
            Kind:         evaluate.Boolean,
            Instructions: "Does `message` request a refund?",
        },
        "department": {
            Kind:         evaluate.Choice,
            Instructions: "Which department should handle `message`?",
            Options: map[string]any{
                "billing":   "Payments and refunds",
                "technical": "Bugs and integration problems",
                "other":     "Anything outside those categories",
            },
        },
    },
}

result, err := client.Evaluate(ctx, request)
if err != nil {
    return err
}

answer := result.Answers["refund_requested"]
if answer.ProbabilityTrue != nil {
    recordRefundProbability(*answer.ProbabilityTrue)
} else if answer.BooleanValue != nil {
    recordRefundDecision(*answer.BooleanValue)
}
```

概率阈值和后续动作由业务代码根据自己的样本决定。替换 provider 前，需要确认业务是否依赖可选概率、元数据或某个具体模型的标定结果。

同一份 `request` 可以通过普通 LLM 执行。以下 `chatModel` 和 `chatAPIKey` 由调用方提供，问题和 State 不变：

```go
chatClient := uniai.New(uniai.Config{
    OpenAIAPIKey:          chatAPIKey,
    EvaluateProvider:      "openai",
    EvaluateModel:         chatModel,
    EvaluateEmulationMode: evaluate.EmulationForce,
})

result, err = chatClient.Evaluate(ctx, request)
if err != nil {
    return err
}

// Emulated 为 true；BooleanValue 存在，ProbabilityTrue 为 nil。
recordRefundDecision(*result.Answers["refund_requested"].BooleanValue)
```

单次请求也可以设置 `Request.EmulationMode` 覆盖默认模式。`fallback` 会根据同一 provider/model 是否有原生适配选择执行方式；它不接受第二套备用 provider/model。

自部署 Qwen 的调用示例沿用同一份问题定义。以下地址、凭证和模型名称均由调用方提供；数值只演示参数传递，不代表足够完成任意请求：

```go
qwenClient := uniai.New(uniai.Config{
    OpenAIAPIBase:         qwenAPIBase,
    OpenAIAPIKey:          qwenAPIKey,
    EvaluateProvider:     "openai",
    EvaluateModel:        qwenModel,
    EvaluateEmulationMode: evaluate.EmulationForce,
})

effort := chat.ReasoningEffortLow
maxTokens := 2048
request.EmulationOptions = &evaluate.EmulationOptions{
    ReasoningEffort: &effort,
    MaxTokens:       &maxTokens,
}

result, err = qwenClient.Evaluate(ctx, request)
if err != nil {
    return err
}
```

服务需要支持所选 effort 的实际含义。也可以把这些参数放入 Config.EvaluateEmulationOptions 作为默认值；请求中的非 nil 字段覆盖对应默认值。

## 第二个 provider 的接入要求

增加适配器前，先用输入输出样本回答四个问题：

1. Boolean 返回的是原生布尔值、成立概率，还是含义不同的分数？
2. Choice 是否确实从调用方提供的选项中选择？是否允许多选或拒答？
3. Score 能否保持请求中等级的顺序和尺度？
4. 一个请求中的多个问题是否各自针对同一状态求值？答案是否独立？若需要拆成多个网络请求，是否有明确的调用次数与部分失败方案？

可以保持契约的能力实现转换；其他能力在调用前返回 Unsupported。公共接口不保证每个 provider 支持全部三类问题。

首版不公开能力列表或通用 `ProviderOptions` 字典。只有实际适配器出现无法表达的需求时，再增加有具体用途的字段；输入字段不能与 Model、问题内容等公共参数形成重复来源。

原生适配按部署接口和答案语义划分，不按训练底模划分。已核对的具体例子如下；它们说明公共三类问题可以复用，不表示已完成服务联调：

| 实现 | 已确认的接口形状 | 接入时需要处理的区别 |
| --- | --- | --- |
| Laya | Python `Agent.predict` 接收 state/questions，返回 Noul、Choice、Score | Go 客户端需要外部 HTTP 服务；不在 uniai 内加载 Python 或模型权重 |
| Kev | 基于 Qwen2.5，提供 `/v1/systemone` | 本地服务不要求 API key；Score 上限和 confidence 算法不同，响应 model 会回显请求值 |
| Nimble | 基于 Qwen3.5，官方 HTTP 部署提供 `/v1/systemone` | 与其原生 Python schema 区分；HTTP 服务支持三类问题，但数量限制和 confidence 定义不同 |

核对依据：[Laya 推理代码](https://github.com/NandhaKishorM/laya/blob/42626c348753fbb17572a813127df2278a1ec527/laya/agent.py)、[Kev 接口转换](https://github.com/jaredpalmer/kev/blob/bd058057ad0aa9df3dd6d14e3542c95a5ce367b2/kev/api.py)、[Kev HTTP 服务](https://github.com/jaredpalmer/kev/blob/bd058057ad0aa9df3dd6d14e3542c95a5ce367b2/kev/serve.py)、[Nimble 部署协议](https://github.com/bespokelabsai/nimble/blob/f136b3f75721fda4ea961f73993cc50b08488835/docs/MODAL_SERVING.md)。

后续接入这些自托管原生服务时，增加显式 `systemone` 兼容入口：base 必填，API key 可选；抽取确实相同的协议转换，保留各服务的限制、精度和元数据差异。不将它们标为 TypeSafe，也不继承 Jev 价格。该入口与 Chat 模拟是两项独立能力；本方案首版实现 TypeSafe 原生路径和 Chat 模拟，不宣称已支持任意 `/v1/systemone` 服务。

用测试中的虚拟 provider 和 Chat 模拟结果检查公共类型是否依赖 Jev 字段；真实第二个原生模型接入时仍需做语义和样本验证。

## 实现步骤与验收

每个 Phase 涉及正式代码前，先添加测试、运行确认预期失败，再实现至通过。纯文档不新增测试。所有自动化测试离线运行，使用内存 HTTP transport 或本地测试服务验证请求和返回响应，不需要数据库或真实 API key。模拟路由测试复用现有 Chat 测试方式，不为测试增加公共 provider registry。

### Phase 1：公共类型和校验

新增 `evaluate/types.go`、`evaluate/validate.go`、`evaluate/errors.go` 及测试。

验收内容：三类合法问题；互斥字段与缺失字段；结构化输入及 null 描述；false/0/缺失值的区别；评分边界；答案 ID 和类型匹配；未知 Kind 和 EmulationMode；MaxTokens 非正值；请求内容不被修改。

用测试代码构造另一个 provider 的结果形状：仅返回 `BooleanValue: false`、Choice 无概率分布、Score 无概率分布、没有 TypeSafe 元数据。这些结果必须通过公共校验；只支持 Boolean 的测试适配器遇到 Score 必须返回 Unsupported。此测试不增加生产代码中的 provider registry。

### Phase 2：TypeSafe HTTP 适配器

新增 `providers/typesafe/typesafe.go` 和 `typesafe_test.go`。

验收内容：endpoint 拼接、鉴权、三种问题混合发送、结构化 criteria 转换、上游模型标识、Noul 概率、分数与分布原值、metadata、usage 和 Raw、Emulated 为 false。测试选项与等级数量上限、响应字段缺失、概率范围、数值舍入、无效 JSON、HTTP 错误、Retry-After、context 取消、body 关闭和响应体限制。对错误和 Debug 输出检查不泄露凭证。

确认同一次 Evaluate 只发一个请求，错误不触发自动重试，context 取消后不继续发送请求。

### Phase 3：根 Client 与调用入口

新增根目录 `evaluate.go` 和 `evaluate_test.go`；更新 `config.go`。类型通过 `evaluate` 包使用，不在 `exports.go` 增加重复构造函数。

验收内容：请求级覆盖、独立默认 provider/model、模式解析、显式 off 覆盖配置、模拟参数逐字段继承与覆盖、显式 none 与缺失的区别、原生路径拒绝请求中的模拟参数、API key 缺失、未知 provider、自定义 base 和原生 HTTP client。`Config.Provider: "openai"` 不能改变 Evaluate 路由；Evaluate 字段也不能改变 Chat 路由。本阶段完成原生调用和配置解析，模拟分支及完整路由矩阵在下一阶段添加测试并实现。先不要求原生费用注解，返回值与独立 provider 一致。

### Phase 4：Chat 模拟与模式选择

新增根目录 `evaluate_emulation.go`、`evaluate_emulation_test.go`，完善根入口路由。复用 `chatOnce`、`annotateChatResultCost` 和单段 JSON 规范化函数，不改写现有 tool calling 模拟流程。

验收内容：

- 用离线替身覆盖 off/fallback/force 与“原生可用、只有 Chat、两者皆无”的组合；force 跳过原生，fallback 只依据发送前的能力判断。配置和公共校验错误不能触发模拟。
- 401、429、超时、无效响应等原生失败后没有第二次调用；模拟失败也不发起修复请求。验证一次模拟只有一次 `chatOnce` 调用，不把底层 SDK 重试计为 Evaluate 新增调用。
- 三类问题混合生成、结构化说明、真假描述、State 完整保留、稳定选项顺序，以及所有问题字段必填的输出定义。
- 接受完整 JSON 和单一代码块；拒绝重复键、额外键、缺失键、null、多个 JSON、夹带正文、截断输出、拒答、意外 tool calls、非法选项和小数或越界等级。
- 模拟结果标记、BooleanValue/Selected/ScoreValue 映射，false/0 保留，概率和 confidence 不被补造，Model 不冒充已解析版本。
- 请求不被修改，ctx 正确传递，Tools/ToolChoice/OnStream 为空，ToolsEmulationMode 关闭。Chat HTTP 配置与原生 EvaluateHTTPClient 的作用范围互不混淆。
- 自定义 Qwen base/model、ReasoningEffort 和 MaxTokens 到 Chat 请求及 HTTP 字段的映射；未设置时不添加参数；已知会忽略显式控制参数的路径返回 Unsupported；原有配置和指针不被修改；达到 token 限额时不补发请求或返回残缺答案。请求映射通过不等于后端确实区分 effort 档位。
- Chat token、缓存明细、已有 warnings 和 Cost 得以保留；InferenceProvider 传递正确；模型或价格未知时不伪造费用。

### Phase 5：原生费用与使用文档

更新 `pricing.go`、`pricing.example.yaml` 和相应测试；根 Evaluate 的原生路径成功后补充费用。新增 `docs/evaluate.md`，在 README 中增加原生与模拟入口示例，并说明三种模式和概率可用性的差异。

验收内容：新版及旧版 YAML、clone 隔离、重复别名、无效价格、provider 隔离、固定模型与别名、未知版本、usage 缺失、零价输出、空 catalog、自定义 catalog。模拟结果不能被原生价格覆盖或重复计价；原生路径也不能使用 Chat 价格。必须保留现有 chat/image 的全部价格测试。

最终运行 `go test ./...`、`go vet ./...` 和 `git diff --check`。真实 API 联调独立进行：使用经授权的测试凭证分别验证一个原生混合问题请求和一个 Chat 模拟请求，并记录模型标识、接口路径及执行方式；离线测试不宣称已验证真实服务可用性。

## 扩展边界

首版提供同步、无状态的原生 Evaluate 和 Chat 模拟。自动拆分批次、流式或异步任务、跨 provider/model 自动切换、概率估计与校准、工作流编排及 Classify 到 Evaluate 的转换，需要各自的实际调用需求再设计。其他模型的特殊输出不能以 TypeSafe 元数据替代必要的公共语义设计。

`cmd/evalbench` 为模型对比单独提供 Jina Classify 转换：用题目描述构造分类标签，将预测标签映射为 Boolean、Choice 或离散等级。该转换仅用于 benchmark，不扩展 `Client.Evaluate` 的 provider 范围或指令执行契约。报告通过 `api_pattern=jina_classify` 区分，Jina 分类分数仅保留为 provider metadata。

本方案中稳定的部分是问题含义、缺失值语义、错误边界和调用方可观察的行为。供应商字段名、精度规则、模型限制和 HTTP 协议属于适配器；新增供应商时不要求调用方接受 Jev 的专有概念。
