#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source_file="$root/docs/agent/instructions.md"
skill_file="$root/.agents/skills/stratum-e2e-development/SKILL.md"

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

for phrase in \
  '.test/verification.yaml' \
  'R0' 'R1' 'R2' 'R3' 'R4' \
  'received' 'scoped' 'classified' 'planned' 'local_verified' \
  'reviewed' 'ci_running' 'attestation_verified' 'accepted' \
  'CI 是唯一验收权威' \
  '重跑只能用于诊断' \
  '规格审查' '代码质量审查' \
  'STATEFUL_E2E_PROFILE=test STATEFUL_E2E_DURATION_SEC=600' \
  'STATEFUL_E2E_PROFILE=release STATEFUL_E2E_DURATION_SEC=3600' \
  'run-scope' 'lease' 'attestation v2'; do
  grep -Fq "$phrase" "$skill_file" || { printf 'unified E2E skill missing: %s\n' "$phrase" >&2; exit 1; }
done

for reference in verification-manifest review-contract failure-taxonomy agent-adapters; do
  test -f "$root/.agents/skills/stratum-e2e-development/references/${reference}.md" || {
    printf 'unified E2E skill reference missing: %s\n' "$reference" >&2
    exit 1
  }
done

bash "$root/scripts/quality/generate-agent-instructions.sh" --check
for generated in "$root/AGENTS.md" "$root/CLAUDE.md"; do
  grep -Fq 'make e2e-system-short' "$generated"
  grep -Fq 'make e2e-attestation-check' "$generated"
  grep -Fq '.test/verification.yaml' "$generated"
  grep -Fq '唯一测试和验收 Skill' "$generated"
done
printf 'system E2E instruction contract tests passed\n'
