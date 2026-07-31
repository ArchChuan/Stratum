#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
manifest=$root/.test/verification.yaml
workflow=$root/.github/workflows/ci.yml

fail() {
  printf 'verification CI contract: %s\n' "$1" >&2
  exit 1
}

grep -Fq 'verification-ci-contract-test' "$workflow" || fail 'CI does not execute its verification contract'

job_for_check() {
  case "$1" in
    static) printf 'static-checks' ;;
    unit) printf 'test' ;;
    integration) printf 'workflow-e2e' ;;
    contract) printf 'contract' ;;
    build) printf 'build' ;;
    security) printf 'security' ;;
    code-quality) printf 'code-quality' ;;
    risk-guardrails) printf 'code-quality' ;;
    *) return 1 ;;
  esac
}

job_exists() {
  grep -Eq "^  $1:" "$workflow"
}

mapfile -t checks < <(sed -n 's/.*ci_checks: \[\(.*\)\]/\1/p' "$manifest" | tr ',' '\n' |
  sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | sort -u)
for check in "${checks[@]}"; do
  job=$(job_for_check "$check") || fail "manifest CI check has no job mapping: $check"
  job_exists "$job" || fail "manifest CI check $check maps to missing job $job"
done

if grep -Eiq 'playwright([^a-z]|$).*install|chromium|e2e-system-(short|soak|release-soak)' "$workflow"; then
  fail 'CI must not install or execute browser E2E'
fi

guardrails=$(sed -n '/^  guardrails:/,/^  lint:/p' "$workflow")
grep -Eq 'if:[[:space:]]*\$\{\{[[:space:]]*always\(\)[[:space:]]*\}\}' <<<"$guardrails" ||
  fail 'Migration Guardrails must evaluate dependencies under always()'
grep -Fq 'toJSON(needs)' <<<"$guardrails" || fail 'Migration Guardrails must inspect every dependency result'
grep -Eq 'all\(\.\[\];[[:space:]]*\.result[[:space:]]*==[[:space:]]*"success"\)' <<<"$guardrails" ||
  fail 'Migration Guardrails must reject non-success dependency results'

printf 'verification CI workflow contract passed\n'
