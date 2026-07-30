#!/usr/bin/env bash
# contract-audit.sh — 契约覆盖快速审计。
# 列出 golden file 覆盖的端点，统计 manifest capabilities，报告明显的缺口。
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
golden_dir="${1:-$repo_dir/api/http/testdata/contracts}"
manifest_file="$repo_dir/test/e2e/stateful/manifest.json"

red()  { printf '\033[31m%s\033[0m\n' "$1"; }
green(){ printf '\033[32m%s\033[0m\n' "$1"; }

echo "=== 契约覆盖审计 ==="
echo ""

# ── Golden file 统计 ─────────────────────────────────────────────────────────
golden_count=0
by_method=''
if [[ -d "$golden_dir" ]]; then
  golden_count=$(find "$golden_dir" -name '*.golden.json' | wc -l)

  echo "--- 按 HTTP 方法统计 ---"
  for m in GET POST PUT PATCH DELETE; do
    count=$(find "$golden_dir" -name "${m,,}_*.golden.json" | wc -l)
    echo "  $m: $count"
  done
  echo "  Total: $golden_count"

  echo ""
  echo "--- 按域统计 ---"
  for domain in agents admin-providers admin-models admin-tenants tenant workflows workflow-runs workflow-approvals \
                skills mcp knowledge memory evaluations conversations auth health dashboard metrics models proposals \
                tool-approvals tool-policies; do
    count=$(find "$golden_dir" -name "*${domain}*.golden.json" 2>/dev/null | wc -l)
    [[ "$count" -gt 0 ]] && echo "  $domain: $count"
  done
fi

# ── Manifest 统计 ────────────────────────────────────────────────────────────
echo ""
echo "--- Manifest Capabilities ---"
if [[ -f "$manifest_file" ]]; then
  cap_count=$(jq '.capabilities | length' "$manifest_file" 2>/dev/null || echo 0)
  domain_count=$(jq '[.capabilities[].domain] | unique | length' "$manifest_file" 2>/dev/null || echo 0)

  echo "  Capabilities: $cap_count"
  echo "  Domains: $domain_count"

  echo ""
  echo "  Per domain:"
  jq -r '[.capabilities[].domain] | group_by(.) | .[] | "\(.[0]): \(length)"' "$manifest_file" 2>/dev/null | sort | while read line; do
    echo "    $line"
  done
fi

# ── 快速覆盖率 ──────────────────────────────────────────────────────────────
echo ""
echo "--- 覆盖率摘要 ---"
if [[ "$golden_count" -gt 0 && "$cap_count" -gt 0 ]]; then
  echo "  Golden files / Manifest caps = $golden_count / $cap_count"
  echo ""
  echo "  注意: golden file 覆盖 HTTP API 端点；manifest 包含前端导航 + API 端点。"
  echo "  这是近似对比，精确审计需要逐端点检查。"
fi

echo ""
green "审计完成。运行 'make contract-enforce' 做完整验证。"
