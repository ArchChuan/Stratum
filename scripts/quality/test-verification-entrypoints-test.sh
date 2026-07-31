#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
makefile=$root/Makefile

for target in plan local ci attestation report; do
  grep -Eq "^test-verify-${target}:" "$makefile" || {
    printf 'missing canonical verification target: test-verify-%s\n' "$target" >&2
    exit 1
  }
done
grep -Fq 'scripts/quality/test-verification-plan.sh' "$makefile"
grep -Fq 'scripts/quality/test-verification-report.sh' "$makefile"
grep -Fq 'scripts/quality/write-verification-check-receipt.sh' "$root/.github/workflows/stateful-e2e.yml"
grep -Fq 'scripts/quality/run-planned-checks.sh' "$root/.github/workflows/stateful-e2e.yml"
grep -Fq 'scripts/quality/run-planned-checks.sh' "$root/.github/workflows/release-verification.yml"
grep -Fq 'env -u STRATUM_TEST_POSTGRES_URL' "$root/scripts/quality/run-planned-checks.sh"
grep -Fq 'STATEFUL_E2E_PROFILE: ""' "$root/.github/workflows/stateful-e2e.yml"
for workflow in stateful-e2e.yml release-verification.yml; do
  if grep -Fq 'run: bash scripts/quality/write-verification-check-receipt.sh \' \
    "$root/.github/workflows/$workflow"; then
    printf 'check receipt command uses a folded YAML scalar: %s\n' "$workflow" >&2
    exit 1
  fi
done
grep -Eq '^test-verify-report:[[:space:]]*$' "$makefile"
grep -Fq 'e2e-attestation-check' "$makefile"
grep -Fq 'verification-schema' "$makefile"

assert_infra_wraps_planned_checks() {
  local workflow=$1
  local up_line planned_line down_line
  up_line=$(grep -n 'run: bash scripts/e2e/ci-infra-up.sh' "$workflow" | head -1 | cut -d: -f1)
  planned_line=$(grep -n 'Run planned non-' "$workflow" | head -1 | cut -d: -f1)
  down_line=$(grep -n 'run: bash scripts/e2e/ci-infra-down.sh' "$workflow" | tail -1 | cut -d: -f1)
  [[ -n "$up_line" && -n "$planned_line" && -n "$down_line" ]]
  (( up_line < planned_line && planned_line < down_line ))
}

assert_infra_wraps_planned_checks "$root/.github/workflows/stateful-e2e.yml"
assert_infra_wraps_planned_checks "$root/.github/workflows/release-verification.yml"

printf 'canonical verification entrypoint contract passed\n'
