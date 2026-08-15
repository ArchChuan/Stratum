#!/usr/bin/env bash
# check-e2e-uncovered-report-test.sh — 自测:合法报告通过、非法报告失败。
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
script="$repo_dir/scripts/quality/check-e2e-uncovered-report.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# 合法报告
cat > "$tmp/valid.json" <<'JSON'
{
  "generated_at": "2026-08-15T00:00:00Z",
  "tested_git_parent": "abc123",
  "route_total": 3,
  "covered": ["GET /memory"],
  "uncovered": [{"method": "POST", "path": "/mcp/servers/:param/reconnect", "domain_hint": "mcp"}],
  "excluded": [{"method": "GET", "path": "/health", "reason": "infra"}]
}
JSON
REPORT_PATH="$tmp/valid.json" "$script" || { echo "expected valid report to pass"; exit 1; }

# 缺 domain_hint 的非法报告
cat > "$tmp/invalid.json" <<'JSON'
{
  "generated_at": "2026-08-15T00:00:00Z",
  "tested_git_parent": "abc123",
  "route_total": 1,
  "covered": [],
  "uncovered": [{"method": "POST", "path": "/x"}],
  "excluded": []
}
JSON
if REPORT_PATH="$tmp/invalid.json" "$script" >/dev/null 2>&1; then
  echo "expected failure for missing domain_hint"; exit 1
fi

# 非法 JSON(截断对象):应单条清晰报错而非 6 条误导的 MISSING。
printf '%s' '{"broken' > "$tmp/broken.json"
if out=$(REPORT_PATH="$tmp/broken.json" "$script" 2>&1); then
  echo "expected failure for invalid JSON"; exit 1
fi
[[ "$out" == *"报告不是合法 JSON 对象"* ]] \
  || { echo "unexpected message for invalid JSON: $out"; exit 1; }

echo "SELFTEST PASS: valid accepted, invalid rejected"
