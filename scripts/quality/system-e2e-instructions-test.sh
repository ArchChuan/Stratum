#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source_file="$root/docs/agent/instructions.md"

for phrase in \
  'make e2e-system-short' \
  'make e2e-system-soak' \
  'make e2e-system-release-soak' \
  'STATEFUL_E2E_PROFILE=test' \
  '无头 Chromium' \
  '测试数据库凭据' \
  'make e2e-attestation-check' \
  'skipped/unreconciled capability' \
  '残留风险' \
  '不能替代系统验收'; do
  grep -Fq "$phrase" "$source_file" || { printf 'system E2E instructions missing: %s\n' "$phrase" >&2; exit 1; }
done

bash "$root/scripts/quality/generate-agent-instructions.sh" --check
for generated in "$root/AGENTS.md" "$root/CLAUDE.md"; do
  grep -Fq 'make e2e-system-short' "$generated"
  grep -Fq 'make e2e-attestation-check' "$generated"
done
printf 'system E2E instruction contract tests passed\n'
