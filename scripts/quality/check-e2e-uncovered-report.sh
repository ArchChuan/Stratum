#!/usr/bin/env bash
# check-e2e-uncovered-report.sh — 校验 uncovered 报告 JSON 形状。
# 报告由 system-stateful.spec.ts 生成,告警级不 gate CI;此脚本守护其 schema,防漂移。
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
report="${REPORT_PATH:-$repo_dir/test/e2e/stateful/uncovered-report.json}"

[[ -f "$report" ]] || { echo "MISSING: $report (run stateful E2E first)"; exit 1; }

issues=0

for field in generated_at tested_git_parent route_total covered uncovered excluded; do
  jq -e "has(\"$field\")" "$report" >/dev/null 2>&1 \
    || { echo "MISSING top-level field: $field"; issues=$((issues + 1)); }
done

jq -e '.uncovered | all(.method != null and .path != null and .domain_hint != null)' "$report" \
  >/dev/null 2>&1 || { echo "uncovered entries must have method/path/domain_hint"; issues=$((issues + 1)); }

jq -e '.excluded | all(.reason != null)' "$report" >/dev/null 2>&1 \
  || { echo "excluded entries must have reason"; issues=$((issues + 1)); }

if [[ "$issues" -eq 0 ]]; then
  echo "PASS: uncovered report shape ok ($(jq -r '.uncovered | length' "$report") uncovered)"
else
  echo "FAIL: $issues uncovered-report shape issue(s)"
  exit 1
fi
