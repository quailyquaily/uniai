#!/usr/bin/env bash

# 从仓库根目录复制模板，填写后加载：
#   cp cmd/evalbench/env.example.sh .env.evalbench.sh
#   # 编辑 .env.evalbench.sh，填写凭证、地址和模型
#   source .env.evalbench.sh
#   go run ./cmd/evalbench --dry-run --limit 18
#   go run ./cmd/evalbench --limit 18
#   go run ./cmd/evalbench --output out/eval-report.json
# 程序不会自动加载 .env 文件；.env.evalbench.sh 已被仓库 .gitignore 排除。

# 通用配置。空值表示不覆盖 provider 的配置或默认行为。
# 比较时使用 --preset 切换模型。预设会覆盖旧的单模型环境参数；
# 显式命令行参数仍可覆盖预设。--preset "" 可恢复自定义配置。
export EVALUATE_PRESET=gpt-4o-mini
export EVALUATE_TIMEOUT=90s
export EVALUATE_API_BASE=""
export EVALUATE_PROVIDER=""
export EVALUATE_MODEL=""
export EVALUATE_EMULATION_MODE=""
export EVALUATE_REASONING_EFFORT=""
export EVALUATE_MAX_TOKENS=""
export EVALUATE_INFERENCE_PROVIDER=""

# GPT-4o mini 与 GPT-5.6 Luna 共用这组凭证。
export OPENAI_API_KEY=""
export OPENAI_API_BASE=https://api.openai.com/v1

# Jev 原生接口凭证。
export TYPESAFE_API_KEY=""
export TYPESAFE_API_BASE=https://api.typesafe.ai/v1

# Jina 分类接口凭证。API base 不包含 /v1。
export JINA_API_KEY=""
export JINA_API_BASE=https://api.jina.ai

# 预设参数：
# gpt-4o-mini   -> openai / force / max_tokens=256 / 不发送 reasoning_effort
# jev          -> typesafe / jev-1.13.0 / off / 不发送生成参数
# gpt-5.6-luna  -> openai / force / max_tokens=256 / reasoning_effort=none
# jina         -> jina / jina-embeddings-v5-text-small / Classify / 不发送生成参数
# 示例（各模型使用同一份完整测试集）：
#   go run ./cmd/evalbench --preset gpt-4o-mini --output out/eval-gpt-4o-mini.json
#   go run ./cmd/evalbench --preset jev --output out/eval-jev.json
#   go run ./cmd/evalbench --preset gpt-5.6-luna --output out/eval-luna-none.json
#   go run ./cmd/evalbench --preset jina --output out/eval-jina.json
# 256 是当前单题案例的生成上限；自定义多题案例可用 --max-tokens 覆盖。

# Jina 可用 --model jina-embeddings-v5-text-nano 切换模型。
# 分类适配只在 benchmark 内使用，不表示 Client.Evaluate 支持 Jina。

# 自定义 Qwen：清除预设后启用下面的配置。
# export EVALUATE_PRESET=""
# export EVALUATE_PROVIDER=openai
# export EVALUATE_MODEL=your-served-qwen-model
# export EVALUATE_EMULATION_MODE=force
# export OPENAI_API_BASE=https://llm.example.com/v1
# export OPENAI_API_KEY=""
# export EVALUATE_REASONING_EFFORT=low
# export EVALUATE_MAX_TOKENS=2048
# export EVALUATE_INFERENCE_PROVIDER=self-hosted
# effort 必须由后端支持；2048 只是示例值，不保证所有案例都能完成。
# 无鉴权的兼容服务也需要非空 OPENAI_API_KEY，可填写服务允许的占位值。

# 其他 Chat provider：清除 EVALUATE_PRESET，设置 provider、model 和 force 模式，
# 再按所选 provider 填写对应凭证。API base 不填时使用 SDK 默认值。
# export GEMINI_API_KEY=""
# export GEMINI_API_BASE=""
# export ANTHROPIC_API_KEY=""
# export ANTHROPIC_API_BASE=""
# export AZURE_OPENAI_API_KEY=""
# export AZURE_OPENAI_ENDPOINT=""
# export AZURE_OPENAI_API_VERSION=""
# export CLOUDFLARE_ACCOUNT_ID=""
# export CLOUDFLARE_API_TOKEN=""
# export CLOUDFLARE_API_BASE=""
# export AWS_ACCESS_KEY_ID=""
# export AWS_SECRET_ACCESS_KEY=""
# export AWS_SESSION_TOKEN=""
# export AWS_REGION=us-east-1

# 案例筛选、重复次数和预热通过命令行参数设置，例如：
#   go run ./cmd/evalbench --category refund_intent --repeat 3 --warmup 2
# 完整参数：go run ./cmd/evalbench --help
