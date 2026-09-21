# Evaluate benchmark

`evalbench` 从环境变量读取模型连接配置，输出每次调用的耗时、实际答案和匹配情况。串行测试 TypeSafe/Jev 原生 Evaluate、Qwen 等模型的 Chat 模拟，以及 Jina Classify 分类基线。

内置 [614 条案例](cases/dataset.jsonl)，共 18 个场景。中文和日文各 258 条，另有英文 90 条、西班牙文 4 条、法文 4 条。数据直接嵌入二进制，从任意工作目录执行都可使用。

## 运行

### 模型对比预设

环境变量模板默认选择 GPT-4o mini。可以通过 `--preset` 切换以下配置，凭证和服务地址继续从对应的环境变量读取：

| 预设 | Provider / 模型 | 模式 | 生成上限 | Reasoning effort |
| --- | --- | --- | --- | --- |
| `gpt-4o-mini` | `openai` / `gpt-4o-mini` | force | 256 | 不发送该参数 |
| `jev` | `typesafe` / `jev-1.13.0` | off | 不发送 | 不发送 |
| `gpt-5.6-luna` | `openai` / `gpt-5.6-luna` | force | 256 | `none` |
| `jina` | `jina` / `jina-embeddings-v5-text-small` | off（使用 Classify） | 不发送 | 不发送 |

```bash
go run ./cmd/evalbench --preset gpt-4o-mini --output out/eval-gpt-4o-mini.json
go run ./cmd/evalbench --preset jev --output out/eval-jev.json
go run ./cmd/evalbench --preset gpt-5.6-luna --output out/eval-luna-none.json
go run ./cmd/evalbench --preset jina --output out/eval-jina.json
```

两组 OpenAI 预设读取 `OPENAI_API_KEY` / `OPENAI_API_BASE`，Jev 读取 `TYPESAFE_API_KEY` / `TYPESAFE_API_BASE`，Jina 读取 `JINA_API_KEY` / `JINA_API_BASE`。四组都默认运行相同的 614 条案例，串行、repeat=1、warmup=0、timeout=90s；Boolean 阈值 0.5，Score 容差 0.5。报告保存预设名称及实际生效参数。

生成上限 256 针对内置的单题短答案；自定义多题案例可用 `--max-tokens` 调整。GPT-4o mini 不发送 reasoning effort，Luna 显式发送 `none`。达到上限的截断结果仍计为错误。

预设覆盖旧的 `EVALUATE_PROVIDER`、`EVALUATE_MODEL`、模式、effort、token 上限和计价提示环境值，防止切换模型时残留错误参数；显式命令行参数优先级更高。`--preset ""` 清除预设后，恢复原有环境变量配置方式。

### Jina 分类基线

Jina 预设复用 SDK 的 `Client.Classify`，调用 `/v1/classify`。v5-text-small、v5-text-nano 和 v3 使用字符串数组作为 input；v4 和 clip-v2 使用 text/image 对象数组。[Jina 接口说明](https://github.com/jina-ai/meta-prompt/blob/main/v12.txt)

分类转换只放在 benchmark 中，SDK 的 `Client.Evaluate` 不增加 Jina provider。报告的 `api_pattern=jina_classify` 表示这条路径，其他组为 `evaluate`。CLI 的 mode=off 只表示关闭 Chat 模拟；转换后的 Jina 结果仍标为 `emulated=true`。

每道问题独立调用一次 Classify，输入固定为 `判断要求：<instructions>\n待分类内容：<state>`。字符串直接使用，结构化内容序列化为 JSON；指令作为分类文本的一部分，不保证模型能执行其中的规则。标签取自题目定义：

- Boolean：使用 `false_description` / `true_description`，将预测标签映射回布尔值。
- Choice：使用选项描述，将预测标签映射回选项 ID。
- Score：使用等级描述，将预测标签映射回从 0 开始的等级索引。

标签必须有非空、互不重复的描述，每题 2–256 类。自定义 Boolean 案例必须提供正反描述；内置 120 条 Boolean 案例已补齐。缺少描述会在发请求前报错，不从 expected 或 rationale 推导标签。数据集指纹随描述更新，比较时四组需要使用相同指纹。

Jina 返回的 score / predictions 保存在 `provider_metadata.jina_classify`，不转成判断概率或题目评分，也不计入 Brier score。usage 只累加 SDK 返回的 total_tokens，不补造输入或输出 token 数；模型名记录请求值，因为 Classify 结果不提供实际模型标识。它不接受 reasoning effort、max tokens 或 Chat 计价提示。

默认 614 条案例均为单题，因此四组各发 614 次请求。自定义多题案例会拆成多个串行分类请求：一次案例的耗时和超时覆盖全部问题，任一问题失败则整条案例失败；此前已成功的问题仍可能产生用量，但失败案例不返回部分答案或完整用量。dry-run 的 requests 显示计划 API 调用数。

可用 `--preset jina --model jina-embeddings-v5-text-nano` 测试 nano。完整成绩之外，所有组还汇总同一份分类子集，见下文统计口径。

### 自定义配置

下面的自定义配置示例需要先清除已加载的预设：

```bash
unset EVALUATE_PRESET
```

从仓库根目录运行。只检查案例和计划调用次数，无须 API key：

```bash
go run ./cmd/evalbench --list --limit 18
go run ./cmd/evalbench --dry-run
```

原生 Jev，先跑 18 条，再跑完整测试集：

```bash
export TYPESAFE_API_KEY="your-api-key"
export EVALUATE_PROVIDER=typesafe
export EVALUATE_MODEL=jev-1.13.0

go run ./cmd/evalbench --limit 18
go run ./cmd/evalbench --output out/eval-jev.json
```

自部署 Qwen：

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_API_BASE="https://llm.example.com/v1"
export EVALUATE_PROVIDER=openai
export EVALUATE_MODEL="your-served-qwen-model"
export EVALUATE_REASONING_EFFORT=low
export EVALUATE_MAX_TOKENS=2048

go run ./cmd/evalbench --limit 18
go run ./cmd/evalbench --output out/eval-qwen-low.json
```

这些地址和模型名是占位值。无鉴权的兼容服务也需要提供非空 API key，占位值需由服务允许。`reasoning_effort` 的实际支持情况和 `max_tokens` 是否包含思考 token，取决于服务；2048 不是保证完成所有请求的预算。

比较同一模型的不同 effort，保持测试集、顺序、重复次数和其他参数相同：

```bash
go run ./cmd/evalbench --reasoning-effort none --seed 42 --repeat 3 --warmup 2 --output out/qwen-none.json
go run ./cmd/evalbench --reasoning-effort low  --seed 42 --repeat 3 --warmup 2 --output out/qwen-low.json
```

一次命令只测一个 provider/model，方便保存独立报告。也可编译后运行：

```bash
go build -o bin/evalbench ./cmd/evalbench
./bin/evalbench --dry-run
```

## 配置

可复制 [env.example.sh](env.example.sh) 为 `.env.evalbench.sh`，填入变量并 `source`。程序读取进程环境，不自动解析 `.env` 文件。

```bash
cp cmd/evalbench/env.example.sh .env.evalbench.sh
# 编辑 .env.evalbench.sh，填写凭证；默认 GPT-4o mini，通过 --preset 切换。
source .env.evalbench.sh
go run ./cmd/evalbench --dry-run --limit 18
go run ./cmd/evalbench --limit 18
```

模板加载时会清除之前的单模型参数，随后应用选定的预设；`.env.evalbench.sh` 已被 Git 忽略。

| 环境变量 | 含义 |
| --- | --- |
| `EVALUATE_PRESET` | 模型对比预设；模板默认 `gpt-4o-mini`，不加载模板时默认空 |
| `EVALUATE_PROVIDER` | 默认 `typesafe` |
| `EVALUATE_MODEL` | 模型；未设置时读取对应 provider 的模型变量 |
| `EVALUATE_EMULATION_MODE` | `off` / `fallback` / `force`；CLI 默认 TypeSafe 和 Jina 用 off，Chat provider 用 force |
| `EVALUATE_API_BASE` | 覆盖对应 provider 的 API base |
| `EVALUATE_REASONING_EFFORT` | 模拟路径的 effort；不设置则不传 |
| `EVALUATE_MAX_TOKENS` | 模拟路径的正整数 token 上限；不设置则不传 |
| `EVALUATE_TIMEOUT` | 单次请求超时，例如 `90s`、`2m`，默认 `90s` |
| `EVALUATE_INFERENCE_PROVIDER` | 模拟路径的 Chat 计价提示，不改变请求目标 |

| Provider | 凭证环境变量 | Base 环境变量 | 模型回退变量 |
| --- | --- | --- | --- |
| `typesafe` | `TYPESAFE_API_KEY` | `TYPESAFE_API_BASE` | `TYPESAFE_MODEL`，再默认 `jev-1.13.0` |
| `jina` | `JINA_API_KEY` | `JINA_API_BASE`，默认 `https://api.jina.ai`，不包含 `/v1` | `JINA_MODEL`，再默认 `jina-embeddings-v5-text-small` |
| `openai`、`openai_resp` | `OPENAI_API_KEY` | `OPENAI_API_BASE` | `OPENAI_MODEL` |
| `gemini` | `GEMINI_API_KEY` | `GEMINI_API_BASE` | `GEMINI_MODEL` |
| `anthropic` | `ANTHROPIC_API_KEY` | `ANTHROPIC_API_BASE` | `ANTHROPIC_MODEL` |
| `azure` | `AZURE_OPENAI_API_KEY` | `AZURE_OPENAI_ENDPOINT`，必填 | `AZURE_OPENAI_DEPLOYMENT` |
| `cloudflare` | `CLOUDFLARE_API_TOKEN`、`CLOUDFLARE_ACCOUNT_ID` | `CLOUDFLARE_API_BASE` | `CLOUDFLARE_MODEL` |
| `bedrock` | `AWS_ACCESS_KEY_ID`、`AWS_SECRET_ACCESS_KEY`；可选 `AWS_SESSION_TOKEN`、`AWS_REGION` | 使用 SDK 默认 endpoint | `BEDROCK_MODEL_ARN` |

也支持 `deepseek`、`xai`、`groq`、`meta`、`sakana`，先读取其大写名称对应的 `*_API_KEY`，缺失时读取 `OPENAI_API_KEY`；模型回退到 `OPENAI_MODEL`。Meta 和 Sakana 可使用 `OPENAI_API_BASE`。DeepSeek、xAI、Groq 的专用 provider 使用固定 endpoint；若环境中已设 `OPENAI_API_BASE`，需要取消该设置，或改用 `openai` 来测试自定义地址。

Azure 另读取 `AZURE_OPENAI_API_VERSION`。CLI 不加载 OAuth 订阅凭证。切换回 TypeSafe 时，需要清除模拟专用变量：

```bash
unset EVALUATE_REASONING_EFFORT EVALUATE_MAX_TOKENS EVALUATE_INFERENCE_PROVIDER EVALUATE_EMULATION_MODE
```

命令行参数覆盖对应环境变量。完整参数见 `go run ./cmd/evalbench --help`：

| 参数 | 默认值及行为 |
| --- | --- |
| `--preset` | `gpt-4o-mini` / `jev` / `gpt-5.6-luna` / `jina`；空字符串恢复自定义配置 |
| `--provider`、`--model`、`--api-base`、`--mode` | 覆盖模型连接和执行方式 |
| `--reasoning-effort`、`--max-tokens`、`--inference-provider` | 模拟参数；空字符串可清除对应环境值 |
| `--timeout` | `90s`，每次请求独立计时 |
| `--cases` | 自定义 JSONL 文件，默认使用内置数据 |
| `--category` | 只选择一个场景 |
| `--domain` | 只选择一个内容领域：`general`、`crypto`、`finance`、`geopolitics` |
| `--language` | 只选择一种语言：`zh`、`ja`、`en`、`es`、`fr` |
| `--limit` | `0` 表示全部；正整数限制案例数 |
| `--seed` | `0` 保留文件顺序；其他整数固定打乱顺序 |
| `--repeat` | `1`；按同一顺序重复完整的已选案例集 |
| `--warmup` | `0`；从已选案例依次取样，必要时循环，预热记录单独保存 |
| `--boolean-threshold` | `0.5`；有原生 bool 时直接使用，否则判断 `ProbabilityTrue >= threshold` |
| `--score-tolerance` | `0.5`；绝对误差不超过该值视为匹配 |
| `--output` | 写入新的 JSON 报告文件；拒绝覆盖已有文件 |
| `--list`、`--dry-run` | 检查数据或调用计划，不发送请求、不要求凭证 |

筛选顺序是 category/domain/language → shuffle → limit。三个筛选条件同时给出时取交集。默认单次完整运行发起 614 次 API 请求；Evaluate 路径的计划调用数为 `已选案例数 × repeat + warmup`，Jina 按每条案例的题数累计。CLI 不增加重试，底层 Chat SDK 的重试仍然适用。正常调用错误会记录并继续，预热失败则停止。Ctrl+C 会取消当前请求、停止后续调用，并尽力保存已有报告；再次中断或强制结束进程不保证保存。

## 测试集

| 类型 | 场景 ID | 内容 |
| --- | --- | --- |
| Boolean | `refund_intent` | 退款、撤销、仅询问、条件与过去事件 |
| Boolean | `negation_scope` | 否定范围、主语、最终更正与时间 |
| Boolean | `eligibility_rules` | 多条件资格、边界值、阻断条件 |
| Boolean | `event_order` | 审批、撤销、重开后的最终状态 |
| Boolean | `evidence_injection` | 权威字段、干扰指令、长备注、JSON 类型区别 |
| Boolean | `deadline_rules` | 截止日包含关系、跨年、闰日、取消 |
| Choice | `support_routing` | 账单、账户、技术、物流、其他 |
| Choice | `content_topic` | 科学、体育、文化、技术、旅行 |
| Choice | `language_detection` | 中文、英文、日文、西班牙文、法文 |
| Choice | `workflow_action` | 升级、补资料、批准、拒绝的优先级 |
| Choice | `intent_classification` | 翻译、摘要、比较、改写、提取 |
| Choice | `moderation_category` | 普通讨论、推广、人身侮辱、邮箱地址 |
| Score | `incident_severity` | 影响比例、区间边界、数据丢失覆盖规则 |
| Score | `sentiment_intensity` | 强烈负面到强烈正面的五级情绪 |
| Score | `form_completeness` | 缺失、null、空白与有效字段数量 |
| Score | `evidence_strength` | 独立来源数量、重复引用和权威反证 |
| Score | `retrieval_relevance` | 无关、同对象、部分回答、完整回答 |
| Score | `change_risk` | 变更规模、特权与审批覆盖规则 |

数据为本仓库编写的合成案例，包含人工编写的短文本、成对反例和明确规则下的结构化边界样本，不来自外部评测集。`language` 是独立评测维度，标注 `state` 中自然语言内容的主要语言，不表示整份提示词只含该语言。新增日文对应案例的判断说明也使用日文；字段名、枚举值等结构化标识保持英文。语言识别场景仍使用中文判断说明，以免提前泄露待识别语言。

中文和日文各 258 条。除语言识别场景已有的 4 条日文外，每条中文案例都有规则、标签和难度对应的日文案例。`domain` 是另一独立维度：`general` 566 条，`crypto`、`finance`、`geopolitics` 各 16 条。三个专题都各含 8 条中文和 8 条日文，并覆盖否定判断、任务分类或支持路由、检索相关性。

原有中文、英文及语言识别案例的标签分布不变；新增日文案例沿用对应中文案例的规则与标签。默认文件前 360 条仍按原有场景交错顺序排列，前 18 条覆盖全部场景和两种布尔标签。

这些样本用于检查接口、比较固定任务下的延迟和发现明显判断错误。它们包含共享规则与模板，不能把 614 条看作 614 个完全独立的真实业务样本；情绪和相关性等级也依赖本测试集的标注约定。比较模型时应同时查看场景结果和实际业务数据。

每行是一个案例，可包含多个问题，字段示例：

```json
{"id":"refund-example","category":"custom","domain":"finance","language":"zh","state":"请退回重复扣款。","questions":{"answer":{"kind":"boolean","instructions":"当前是否请求退款？"}},"expected":{"answer":true},"rationale":"明确请求返还已经支付的钱。"}
```

`domain` 和 `language` 必填；`domain` 必须使用上述四个值，`language` 可使用自定义语言标签。`expected` 必须完整覆盖 question ID。Boolean 标签为 JSON bool，Choice 为合法选项 ID，Score 为合法整数等级索引。加载时检查格式、重复案例 ID、问题定义和标签。`expected`、`rationale`、案例 ID、category、domain 和 language 都不会发给模型。

## 结果与统计口径

每次测量输出实际响应模型、执行方式、毫秒耗时和答案，包括原生概率或小数评分。汇总输出总调用数、成功数、错误数、答案匹配数、成功调用和错误调用各自的 mean / P50 / P95 / min / max，以及各场景结果。百分位采用 nearest-rank：排序后取 `ceil(p × N)` 项。

耗时覆盖整个 `Client.Evaluate` 调用或一条案例的 Jina 分类转换，包括请求构造、网络、上游计算、响应解析及 SDK 内部重试，不包含 benchmark 的预期答案比较和报告写入。它不是服务端推理耗时，也不是首 token 延迟。串行执行，未测并发吞吐。

`classification_summary` 和终端的 `classification_subset` 汇总 8 个固定场景：refund_intent、support_routing、content_topic、language_detection、intent_classification、moderation_category、sentiment_intensity、retrieval_relevance，共 234 条内置案例。它们侧重语义分类，但仍可能涉及否定、条件和上下文。其余日期、计数和规则案例继续计入完整 summary。报告和终端同时按 category、domain、language 汇总，便于直接比较中文与日文或三个专题。这个分组对所有模型相同，不根据模型结果筛选；自定义案例按相同 category 名归组，其他 category 仅进入完整成绩。

预热不计入正式统计。`wall_time_ms` 包含预热和运行中终端输出，排除最终汇总与报告写入；不要把它当作纯 API 耗时。重复案例可能命中缓存，预热和 repeat 的设置需保持一致，usage 中可用的缓存明细会保留。

JSON 报告包含：

- 数据集完整文件的 SHA-256、实际选中案例 ID 和顺序、seed、请求模型、执行模式、effort 和 token 限额。
- 每次成功响应的答案、usage、provider metadata，以及各题 expected / actual / match；不序列化 `Result.Raw`。
- `accuracy`：匹配题数 / 所有已尝试题数，调用失败计为未匹配。
- `valid_accuracy`：仅在有效响应内计算的匹配率，便于区分判断错误和请求失败。
- `score_mae`：有效 Score 答案的平均绝对误差；`probability_count` / `brier_mean`：实际提供 Boolean 概率的样本数和平均 Brier score。模拟结果不编造概率。
- 取消时保存已尝试部分的统计；未执行案例不会被计成错误。

阈值判断只发生在 benchmark 中，不改变 SDK 的概率语义。Jev 的小数期望评分与 Chat 的整数选择并不等价，因此同时保留原始值、MAE 和容差匹配率。Brier score 只是一项概率误差指标，不能单独证明模型已校准。

报告不保存 API key、连接配置或输入文件的本机路径。输出文件权限为 `0600`。一次完整运行即使存在判断不匹配，只要调用均有效仍退出 0；配置错误、调用错误、非法响应或取消退出 1，并保留可用报告。

## 离线验证

```bash
go test ./cmd/evalbench
go vet ./cmd/evalbench
```

测试使用内存回调和本地 HTTP 服务，覆盖原生/模拟请求、参数与环境变量、标签隔离、评分、失败统计、取消、超时、数据覆盖范围及报告输出，不需要真实模型凭证。
