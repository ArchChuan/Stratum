#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
plan=${TEST_VERIFY_PLAN_PATH:-$root/tmp/test-verification/plan.json}
plan_command=${TEST_VERIFY_PLAN_COMMAND:-$root/scripts/quality/test-verification-plan.sh}
checks_command=${TEST_VERIFY_CHECKS_COMMAND:-$root/scripts/quality/run-planned-checks.sh}
eval_command=${TEST_VERIFY_EVAL_COMMAND:-$root/scripts/quality/run-eval-checks.sh}
make_command=${TEST_VERIFY_MAKE_COMMAND:-make}
writer=${LOCAL_VERIFY_WRITER:-$root/scripts/quality/write-local-verification-report.sh}
checker=${LOCAL_VERIFY_CHECKER:-$root/scripts/quality/check-local-verification-report.sh}
attestation_dir=${E2E_ATTESTATION_DIR:-$root/test/e2e/attestations}

run_short() {
  "$make_command" e2e-system-short
}

run_soak() {
  STATEFUL_E2E_PROFILE=test STATEFUL_E2E_DURATION_SEC=600 STATEFUL_E2E_PACKS=all \
    "$make_command" e2e-system-soak
}

run_release_soak() {
  "$make_command" e2e-system-release-soak
}

run_browser_mode() {
  local mode=$1
  [[ "$mode" == none ]] && return 0
  run_short || return
  [[ "$mode" == short ]] && return 0
  run_soak || return
  [[ "$mode" == soak ]] && return 0
  run_release_soak
}

select_attestation() {
  local commit=$1 digest=$2 candidate
  [[ -n "${LOCAL_VERIFY_ATTESTATION_PATH:-}" ]] && return 0
  while IFS= read -r candidate; do
    if jq -e --arg commit "$commit" --arg digest "${digest#sha256:}" '
      .tested_git_parent == $commit and .policy_manifest_digest == $digest and .schema_version == 2
    ' "$candidate" >/dev/null 2>&1; then
      export LOCAL_VERIFY_ATTESTATION_PATH=$candidate
      return 0
    fi
  done < <(find "$attestation_dir" -type f -name '*.json' -printf '%T@ %p\n' 2>/dev/null | sort -rn | cut -d' ' -f2-)
  return 1
}

record_outcome() {
  local outcome=$1
  LOCAL_VERIFY_OUTCOME=$outcome bash "$writer"
}

bash "$plan_command" >/dev/null
[[ -f "$plan" ]] || { printf 'before-PR verification plan is missing: %s\n' "$plan" >&2; exit 1; }
mode=$(jq -er .mode "$plan")
commit=$(jq -er .commit "$plan")
manifest_digest=$(jq -er .manifest_digest "$plan")

first_failure=0
bash "$checks_command" || first_failure=$?
if ((first_failure == 0)); then
  bash "$eval_command" || first_failure=$?
fi
if ((first_failure == 0)); then
  run_browser_mode "$mode" || first_failure=$?
fi

outcome=passed
if ((first_failure != 0)); then
  outcome=failed
fi
if [[ "$mode" != none ]] && ! select_attestation "$commit" "$manifest_digest"; then
  outcome=infra_failed
  ((first_failure != 0)) || first_failure=1
fi
record_outcome "$outcome" >/dev/null || { ((first_failure != 0)) || first_failure=$?; }
if ((first_failure == 0)); then
  bash "$checker" >/dev/null || first_failure=$?
fi
exit "$first_failure"
