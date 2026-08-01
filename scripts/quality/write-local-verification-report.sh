#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
plan=${TEST_VERIFY_PLAN_PATH:-$root/tmp/test-verification/plan.json}
output=${LOCAL_VERIFY_REPORT_PATH:-$root/tmp/test-verification/local-verification.json}
history_dir=${LOCAL_VERIFY_HISTORY_DIR:-$root/tmp/test-verification/history}
attestation=${LOCAL_VERIFY_ATTESTATION_PATH:-}
source_root=${LOCAL_VERIFY_SOURCE_ROOT:-$root}
manifest=${LOCAL_VERIFY_MANIFEST_PATH:-$source_root/.test/verification.yaml}
schema=${LOCAL_VERIFY_SCHEMA_PATH:-$root/.test/schemas/local-verification.schema.json}
outcome=${LOCAL_VERIFY_OUTCOME:-passed}

fail() {
  printf 'local verification report: %s\n' "$1" >&2
  exit 1
}

source_is_clean() {
  local changed
  changed=$(git -C "$source_root" status --porcelain --untracked-files=all | awk '
    {path=substr($0,4)}
    path !~ /^test\/e2e\/attestations\// {print path}
  ')
  [[ -z "$changed" ]]
}

relative_attestation_path() {
  if [[ -z "$attestation" ]]; then
    return
  fi
  if [[ "$attestation" == "$source_root/"* ]]; then
    printf '%s' "${attestation#"$source_root/"}"
    return
  fi
  printf '%s' "$attestation"
}

verify_attestation() {
  local commit=$1 digest=$2 mode=$3 required_mode=$3 required_profile=
  [[ -n "$attestation" && -f "$attestation" ]] || fail 'attestation is missing'
  case "$mode" in
    soak) required_profile=test ;;
    release-soak) required_mode=soak; required_profile=release ;;
  esac
  jq -e --arg commit "$commit" --arg digest "${digest#sha256:}" '
    .tested_git_parent == $commit and .policy_manifest_digest == $digest and .schema_version == 2
  ' "$attestation" >/dev/null || fail 'attestation identity does not match the plan'
  local args=(verify --root "$source_root" --ref HEAD --required-mode "$required_mode")
  [[ -z "$required_profile" ]] || args+=(--required-profile "$required_profile")
  args+=(--attestation "$attestation")
  go run "$root/cmd/e2e-attestation" "${args[@]}" >/dev/null || fail 'attestation verification failed'
}

summarize_attestation() {
  capabilities=$(jq '
    def count_status($status): [.capabilities[] | select(.status == $status)] | length;
    {passed:count_status("passed"),failed:count_status("failed"),blocked:count_status("blocked"),
     skipped:count_status("skipped"),unreconciled:(.unverified_capabilities | length)}
  ' "$attestation")
  cleanup=$(jq '{complete:(.cleanup.passed and .owned_cleanup.database_dropped and .owned_cleanup.lease_removed),
    residual_entities:(.cleanup.residual_entity_ids | length)}' "$attestation")
}

preserve_previous_failure() {
  [[ -f "$output" ]] || return 0
  jq -e '.status == "failed" or .status == "infra_failed"' "$output" >/dev/null 2>&1 || return 0
  mkdir -p "$history_dir"
  cp "$output" "$history_dir/$(date -u +%Y%m%dT%H%M%S%N)-$(jq -r .status "$output").json"
}

write_report() {
  local status=$1 clean=$2 path=$3 temporary
  mkdir -p "$(dirname "$output")"
  temporary=$(mktemp "$(dirname "$output")/.local-verification.XXXXXX")
  jq -n --arg status "$status" --arg commit "$commit" --arg digest "$manifest_digest" \
    --arg risk "$risk" --arg mode "$mode" --arg path "$path" --argjson clean "$clean" \
    --argjson capabilities "$capabilities" --argjson cleanup "$cleanup" '
    {version:1,status:$status,tested_commit:$commit,manifest_digest:$digest,risk_level:$risk,mode:$mode,
     source_clean:$clean,capabilities:$capabilities,cleanup:$cleanup,attestation_path:$path}
  ' >"$temporary"
  mv "$temporary" "$output"
}

[[ -f "$plan" ]] || fail "plan is missing: $plan"
[[ -f "$manifest" ]] || fail "manifest is missing: $manifest"
case "$outcome" in passed | failed | not_run | infra_failed) ;; *) fail "unsupported outcome: $outcome" ;; esac
commit=$(jq -er .commit "$plan")
manifest_digest=$(jq -er .manifest_digest "$plan")
risk=$(jq -er .risk_level "$plan")
mode=$(jq -er .mode "$plan")
actual_digest=sha256:$(sha256sum "$manifest" | awk '{print $1}')
[[ "$manifest_digest" == "$actual_digest" ]] || fail 'plan manifest digest is stale'

capabilities='{"passed":0,"failed":0,"blocked":0,"skipped":0,"unreconciled":0}'
cleanup='{"complete":true,"residual_entities":0}'
if [[ "$mode" != none && "$outcome" != infra_failed && "$outcome" != not_run ]]; then
  verify_attestation "$commit" "$manifest_digest" "$mode"
  summarize_attestation
elif [[ "$outcome" == infra_failed ]]; then
  attestation=
  cleanup='{"complete":false,"residual_entities":0}'
fi
clean=true
source_is_clean || clean=false
status=$outcome
blocking=$(jq '[.failed,.blocked,.skipped,.unreconciled] | add' <<<"$capabilities")
cleanup_ok=$(jq -r '.complete and .residual_entities == 0' <<<"$cleanup")
if [[ "$status" == passed && ("$clean" != true || "$blocking" -ne 0 || "$cleanup_ok" != true) ]]; then
  status=failed
fi

preserve_previous_failure
write_report "$status" "$clean" "$(relative_attestation_path)"
go run "$root/cmd/verification-schema" --schema "$schema" --input "$output" >/dev/null
printf '%s\n' "$output"
[[ "$status" == "$outcome" ]]
