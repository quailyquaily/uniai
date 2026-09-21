#!/usr/bin/env bash
# compare.sh — 跑四个默认预设（同一份 614 条数据集）并横向对比。
#
# 用法（在仓库任意目录执行）：
#   bash cmd/evalbench/compare.sh                 # 全量 614 条
#   bash cmd/evalbench/compare.sh --limit 18      # 先用 18 条冒烟
#   bash cmd/evalbench/compare.sh --repeat 3 --warmup 2
#   bash cmd/evalbench/compare.sh --help
#
# 凭证（按预设需要的环境变量，缺少对应 key 的预设会被跳过）：
#   gpt-4o-mini / gpt-5.6-luna  -> OPENAI_API_KEY（共用 OPENAI_API_BASE）
#   jev                          -> TYPESAFE_API_KEY
#   jina                         -> JINA_API_KEY
# 参考 cmd/evalbench/env.example.sh。
#
# 报告写到 $EVALBENCH_OUT（默认 out/）：eval-<preset>.json，
# 文件已存在时会报错退出，不会覆盖之前的结果。
set -euo pipefail

usage() {
  sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'
  exit 0
}
[[ "${1:-}" == "-h" || "${1:-}" == "--help" ]] && usage

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$ROOT"

OUT_DIR="${EVALBENCH_OUT:-out}"
PRESETS=(gpt-4o-mini jev gpt-5.6-luna jina)
KEY_ENV=(OPENAI_API_KEY TYPESAFE_API_KEY OPENAI_API_KEY JINA_API_KEY)

if [[ $# -gt 0 ]]; then
  EXTRA=("$@")
else
  EXTRA=()
fi

for i in "${!PRESETS[@]}"; do
  preset="${PRESETS[$i]}"
  key_env="${KEY_ENV[$i]}"
  out="$OUT_DIR/eval-$preset.json"
  if [[ -z "${!key_env:-}" ]]; then
    echo "skip $preset: $key_env 未设置" >&2
    continue
  fi
  if [[ -e "$out" ]]; then
    echo "skip $preset: $out 已存在（删除后可重跑）" >&2
    continue
  fi
  echo "=== preset=$preset -> $out ===" >&2
  go run ./cmd/evalbench --preset "$preset" --output "$out" "${EXTRA[@]}" \
    || echo "fail $preset: 本次运行失败，继续跑其余预设" >&2
done

reports=()
for preset in "${PRESETS[@]}"; do
  f="$OUT_DIR/eval-$preset.json"
  [[ -f "$f" ]] && reports+=("$f")
done

if [[ ${#reports[@]} -lt 2 ]]; then
  echo "报告不足两份（需要至少两个预设的 key），无法对比" >&2
  exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "未找到 jq，无法生成对比表；报告：${reports[*]}" >&2
  exit 0
fi

printf 'PRESET\tMODEL\tPATTERN\tACCURACY\tVALID_ACC\tSCORE_MAE\tBRIER\tFAILED\tP50_MS\tP95_MS\tWALL_MS\n'
jq -rs '
  map({
    preset,
    model: .requested_model,
    pattern: .api_pattern,
    acc: (.summary.accuracy // "n/a"),
    vacc: (.summary.valid_accuracy // "n/a"),
    mae: (.summary.score_mae // "n/a"),
    brier: (.summary.brier_mean // "n/a"),
    failed: (.summary.failed // "n/a"),
    p50: (.summary.success_latency.p50_ms // "n/a"),
    p95: (.summary.success_latency.p95_ms // "n/a"),
    wall: (.wall_time_ms // "n/a")
  } | [.preset, .model, .pattern, .acc, .vacc, .mae, .brier, .failed, .p50, .p95, .wall] | @tsv)
  | .[]' "${reports[@]}"
echo
echo "报告：${reports[*]}"
echo "逐题对比：jq '.attempts[] | select(.checks[] | select(.match | not))' <report>"
