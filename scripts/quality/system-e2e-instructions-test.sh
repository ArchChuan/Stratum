#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source_file=$root/docs/agent/instructions.md
skill_file=$root/.agents/skills/stratum-e2e-development/SKILL.md

require_phrase() {
  local file=$1 phrase=$2 label=$3
  grep -Fq "$phrase" "$file" || { printf '%s missing: %s\n' "$label" "$phrase" >&2; exit 1; }
}

reject_pattern() {
  local pattern=$1
  if grep -Eiq "$pattern" "$skill_file" "$source_file" "$root/docs/agent/e2e-standards.md" \
    "$root/docs/agent/verification-ci-authority.md" \
    "$root/.agents/skills/stratum-e2e-development/references/"*.md; then
    printf 'obsolete E2E authority claim remains: %s\n' "$pattern" >&2
    exit 1
  fi
}

for phrase in \
  'make test-verify-before-pr' \
  'STATEFUL_E2E_PROFILE=test STATEFUL_E2E_DURATION_SEC=600 STATEFUL_E2E_PACKS=all' \
  'make e2e-system-release-soak' \
  '无头 Chromium' \
  '测试数据库凭据' \
  'skipped/unreconciled capability' \
  '不能替代系统验收'; do
  require_phrase "$source_file" "$phrase" 'system E2E instructions'
done

for phrase in \
  '.test/verification.yaml' \
  'browser_e2e_authority: local' \
  'merge_authority: ci' \
  'deployment_authority: release_pipeline' \
  'make test-verify-before-pr' \
  'audit assertion' \
  'R0' 'R1' 'R2' 'R3' 'R4' \
  '600' '3600' 'run-scope' 'lease' 'attestation v2'; do
  require_phrase "$skill_file" "$phrase" 'unified E2E skill'
done

reject_pattern 'CI 是唯一验收权威'
reject_pattern 'CI.*(headless Chromium|无头 Chromium|browser E2E|浏览器 E2E)'
reject_pattern '(stateful-e2e|platform-assistant-browser-e2e).*(job|门禁)'
reject_pattern 'github-actions-sigstore|OIDC/Sigstore.*(browser|attestation|运行)'
reject_pattern 'protected environment|受保护.*environment.*review receipt'

for reference in verification-manifest failure-taxonomy agent-adapters; do
  test -f "$root/.agents/skills/stratum-e2e-development/references/${reference}.md" || {
    printf 'unified E2E skill reference missing: %s\n' "$reference" >&2
    exit 1
  }
done

skill_count=$(find "$root/.agents/skills" -mindepth 1 -maxdepth 1 -type d -name '*e2e*' | wc -l)
[[ "$skill_count" -eq 1 ]] || { printf 'expected one repository E2E Test Skill, found %s\n' "$skill_count" >&2; exit 1; }

bash "$root/scripts/quality/generate-agent-instructions.sh" --check
for generated in "$root/AGENTS.md" "$root/CLAUDE.md"; do
  require_phrase "$generated" 'make test-verify-before-pr' 'generated Agent instructions'
  require_phrase "$generated" '.test/verification.yaml' 'generated Agent instructions'
  require_phrase "$generated" '唯一测试和验收 Skill' 'generated Agent instructions'
done
printf 'system E2E instruction contract tests passed\n'
