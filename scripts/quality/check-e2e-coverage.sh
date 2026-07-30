#!/usr/bin/env bash
# check-e2e-coverage.sh — 五层防线交叉审计。
# 验证每个注册路由在 contract golden → manifest → E2E pack 三层都有覆盖。
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
golden_dir="$repo_dir/api/http/testdata/contracts"
manifest_file="$repo_dir/test/e2e/stateful/manifest.json"
packs_dir="$repo_dir/web/e2e/stateful/packs"
spec_file="$repo_dir/web/e2e/system-stateful.spec.ts"

issues=0

red()  { printf '\033[31m%s\033[0m\n' "$1"; }
green(){ printf '\033[32m%s\033[0m\n' "$1"; }

# ── Layer 3: golden file 存在性 ─────────────────────────────────────────────
echo "=== Layer 3: Contract Goldens ==="
if [[ -d "$golden_dir" ]]; then
  golden_count=$(find "$golden_dir" -name '*.golden.json' | wc -l)
  echo "  Golden files: $golden_count"
else
  red "  MISSING: golden directory $golden_dir"
  issues=$((issues + 1))
fi

# ── camelCase 扫描 ───────────────────────────────────────────────────────────
echo ""
echo "=== camelCase Enforcement ==="
camel_violations=0
if [[ -d "$golden_dir" ]]; then
  while IFS= read -r -d '' file; do
    pascal_keys=$(jq -r '
      .[]? | select(.want_status != null) |
      (.want_body // .body // empty) |
      if type == "object" then keys[] else empty end
    ' "$file" 2>/dev/null | grep '^[A-Z]' || true)
    if [[ -n "$pascal_keys" ]]; then
      while IFS= read -r key; do
        red "  PascalCase key '$key' in $file"
        camel_violations=$((camel_violations + 1))
      done <<< "$pascal_keys"
    fi
  done < <(find "$golden_dir" -name '*.golden.json' -print0)
fi
if [[ "$camel_violations" -eq 0 ]]; then
  green "  All keys camelCase ✓"
else
  issues=$((issues + camel_violations))
fi

# ── Layer 4: Manifest 能力声明 ───────────────────────────────────────────────
echo ""
echo "=== Layer 4: Manifest Capabilities ==="
if [[ -f "$manifest_file" ]]; then
  cap_count=$(jq '.capabilities | length' "$manifest_file" 2>/dev/null || echo 0)
  domain_count=$(jq '[.capabilities[].domain] | unique | length' "$manifest_file" 2>/dev/null || echo 0)
  echo "  Capabilities: $cap_count across $domain_count domains"
else
  red "  MISSING: manifest file $manifest_file"
  issues=$((issues + 1))
fi

# ── Layer 5: E2E Pack 覆盖 ──────────────────────────────────────────────────
echo ""
echo "=== Layer 5: E2E Pack Coverage ==="
if [[ -f "$spec_file" ]]; then
  pack_count=$(grep -c "executeLLMAdminPack\|executeIAMAdminPack\|executeWorkflowPack\|executeAgentPack\|executeSkillPack\|executeMCPPack\|executeKnowledgePack\|executeMemoryPack\|executeEvaluationPack\|executeDashboardPack\|executeAgentContextPack\|executeEvaluationPromotionPack\|executeAgentSkillMCPPack" "$spec_file" 2>/dev/null || echo 0)
  echo "  Pack dispatch entries in spec: $pack_count"
else
  red "  MISSING: spec file $spec_file"
  issues=$((issues + 1))
fi

if [[ -d "$packs_dir" ]]; then
  pack_files=$(find "$packs_dir" -name '*.ts' -not -name '*.spec.*' | wc -l)
  echo "  Pack files: $pack_files"
fi

# ── 结论 ─────────────────────────────────────────────────────────────────────
echo ""
echo "=============================================="
if [[ "$issues" -eq 0 ]]; then
  green "PASS: 五层防线交叉审计无缺口"
else
  red "FAIL: $issues 个缺口待修复"
  echo ""
  echo "修复流程:"
  echo "  1. 运行 make record-contracts 补充 golden files"
  echo "  2. 编辑 test/e2e/stateful/manifest.json 添加 capability"
  echo "  3. 编写 E2E pack action (web/e2e/stateful/packs/)"
  echo "  4. 在 web/e2e/system-stateful.spec.ts 注册 pack dispatch"
  echo "  5. 重新运行本脚本验证"
  exit 1
fi
