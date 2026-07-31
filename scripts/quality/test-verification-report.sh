#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
plan=${TEST_VERIFY_PLAN_PATH:-$root/tmp/test-verification/plan.json}
output=${TEST_VERIFY_REPORT_PATH:-$root/tmp/test-verification/completion-report.json}
attestation_dir=${E2E_ATTESTATION_DIR:-$root/test/e2e/attestations}
[[ -f "$plan" ]] || { printf 'verification plan is missing: %s\n' "$plan" >&2; exit 1; }

mode=$(jq -er .mode "$plan"); risk=$(jq -er .risk_level "$plan")
required_mode=$mode; required_profile=
[[ "$mode" == release-soak ]] && { required_mode=soak; required_profile=release; }
[[ "$mode" == soak ]] && required_profile=test
[[ "$mode" == none ]] && { printf 'verification mode none has no attestation report\n' >&2; exit 1; }
digest=$(go run "$root/cmd/e2e-attestation" digest --root "$root" --ref HEAD)
mapfile -t candidates < <(find "$attestation_dir" -type f -name "$digest.json" -print | sort)
selected=
for candidate in "${candidates[@]}"; do
  args=(verify --root "$root" --ref HEAD --required-mode "$required_mode" --attestation "$candidate")
  [[ -z "$required_profile" ]] || args+=(--required-profile "$required_profile")
  if go run "$root/cmd/e2e-attestation" "${args[@]}" >/dev/null 2>&1; then selected=$candidate; break; fi
done
[[ -n "$selected" ]] || { printf 'no verified attestation is available for completion reporting\n' >&2; exit 1; }

spec=${TEST_VERIFY_SPEC_REVIEW:-not_required}; quality=${TEST_VERIFY_QUALITY_REVIEW:-not_required}
release=${TEST_VERIFY_RELEASE_REVIEW:-not_required}; status=incomplete; reviews_ok=true
[[ "$risk" == R0 ]] || [[ "$quality" == passed ]] || reviews_ok=false
[[ "$risk" == R0 || "$risk" == R1 ]] || [[ "$spec" == passed ]] || reviews_ok=false
[[ "$risk" != R4 ]] || [[ "$release" == passed ]] || reviews_ok=false
[[ "${CI:-false}" == true && "$reviews_ok" == true ]] && status=accepted

mkdir -p "$(dirname "$output")"
temporary=$output.tmp
jq -n --argjson plan "$(cat "$plan")" --argjson attestation "$(cat "$selected")" \
  --arg status "$status" --arg path "$selected" --arg spec "$spec" --arg quality "$quality" --arg release "$release" \
  --arg artifact "sha256:$(sha256sum "$selected" | cut -d' ' -f1)" '
  def count_status($s): [$attestation.capabilities[] | select(.status == $s)] | length;
  {version:1,status:$status,commit:$plan.commit,manifest_digest:$plan.manifest_digest,
   risk_level:$plan.risk_level,mode:$plan.mode,
   reviews:{specification:$spec,"code-quality":$quality,"release-evidence":$release},
   capabilities:{passed:count_status("passed"),failed:count_status("failed"),blocked:count_status("blocked"),skipped:count_status("skipped"),unreconciled:($attestation.unverified_capabilities|length)},
   attestation:{schema:$attestation.schema_version,verified:true,path:$path},
   cleanup:{complete:($attestation.cleanup.passed and $attestation.owned_cleanup.database_dropped and $attestation.owned_cleanup.lease_removed),residual_entities:($attestation.cleanup.residual_entity_ids|length)},
   artifacts:[$artifact]}' >"$temporary"
mv "$temporary" "$output"
go run "$root/cmd/verification-schema" --schema "$root/.test/schemas/completion-report.schema.json" --input "$output"
[[ "${TEST_VERIFY_REQUIRE_ACCEPTED:-false}" != true || "$status" == accepted ]] || {
  printf 'CI completion report is not accepted\n' >&2; exit 1
}
printf '%s\n' "$output"
