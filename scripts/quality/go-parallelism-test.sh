#!/usr/bin/env bash
set -euo pipefail

# 自测：go-parallelism.sh 的分档契约（12 核基准，公式 p = min(4, max(1, nproc−5−ceil(load1))))

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="${ROOT}/scripts/quality/go-parallelism.sh"

expect() {
  local load=$1 nproc=$2 want=$3
  local got
  got=$(GO_PARALLELISM_NPROC="$nproc" bash "$SCRIPT" "$load")
  [[ "$got" == "$want" ]] || {
    echo "load=${load} nproc=${nproc}: expected ${want}, got ${got}" >&2
    exit 1
  }
}

# 12 核分档（ceil 语义）：load ≤ 3 → 4；3 < load ≤ 4 → 3；4 < load ≤ 5 → 2；> 5 → 1
expect 1 12 4
expect 3 12 4
expect 3.5 12 3
expect 4 12 3
expect 4.9 12 2
expect 5 12 2
expect 6 12 1
expect 8.2 12 1

# 上限 4：空闲（load 0）不得超 4
expect 0 12 4
expect 0 64 4

# 下限 1：高负载/低核永不归零
expect 99 12 1
expect 0 4 1
expect 0 2 1

# 防御：非数字 load / 非法 nproc → 退化为 4（与现状一致）
got=$(GO_PARALLELISM_NPROC=abc bash "$SCRIPT" not-a-number)
[[ "$got" == "4" ]] || { echo "invalid input: expected 4, got ${got}" >&2; exit 1; }

# 真实 /proc/loadavg 路径（仅验证可运行且输出在 [1,4]）
got=$(bash "$SCRIPT")
[[ "$got" =~ ^[1-4]$ ]] || { echo "real loadavg path: expected [1,4], got ${got}" >&2; exit 1; }

echo 'go-parallelism behavior passed'
