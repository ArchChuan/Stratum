#!/usr/bin/env bash
set -euo pipefail

# go 工具链并行度：p = min(4, max(1, nproc − 5 − ceil(loadavg1)))。
# 用意：多 worktree 并行跑全量验证时 CPU 被打满。空闲时全速（p=4，
# 与手动 `go test -p 4` 同速），已有负载（其他 worktree 在编译）时
# 自动退让到 3/2/1，保证常驻服务 + 2 个 worktree 的峰值 ≤ ~85% 核。
# 输出仅数字（供 `go test -p N` 使用），读失败/非 Linux 退化输出 4。
#
# 自测：bash scripts/quality/go-parallelism.sh 4   # 注入 load=4 → 3
# 覆盖：GO_PARALLELISM_NPROC=8 注入 nproc 值（自测用）。

if [[ $# -ge 1 ]]; then
  load=$1
elif [[ -r /proc/loadavg ]]; then
  load=$(awk '{print $1}' /proc/loadavg)
else
  # 非 Linux 或无法读取：退化不限制（与现状一致，宁可不限也不破坏构建）
  echo 4
  exit 0
fi

nproc_val=${GO_PARALLELISM_NPROC:-$(nproc)}

# 防御：注入值非法时显式退化为 4（不限制，等价现状），不代入公式
[[ "$load" =~ ^[0-9]+(\.[0-9]+)?$ ]] || { echo 4; exit 0; }
[[ "$nproc_val" =~ ^[1-9][0-9]*$ ]] || { echo 4; exit 0; }

# ceil(loadavg1)：awk 处理整数/小数（printf %.0f 的轮入语义不匹配 ceil）
load_ceil=$(awk -v l="$load" 'BEGIN { c = (l == int(l)) ? l : int(l) + 1; print c }')

p=$((nproc_val - 5 - load_ceil))
[[ "$p" -gt 4 ]] && p=4
[[ "$p" -lt 1 ]] && p=1
echo "$p"
