#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
output=${TEST_VERIFY_PLAN_PATH:-$root/tmp/test-verification/plan.json}
base_ref=${TEST_VERIFY_BASE_REF:-origin/main}
mapfile -t changed < <(git -C "$root" diff --name-only "$base_ref"...HEAD)
risk=${TEST_VERIFY_RISK_LEVEL:-}
if [[ -z "$risk" ]]; then
  if ((${#changed[@]} == 0)); then risk=R0
  elif [[ $(bash "$root/scripts/quality/risk-regression-guard.sh" --acceptance "${changed[@]}") == soak ]]; then risk=R3
  else risk=R2
  fi
fi

case "$risk" in
  R0) mode=none; checks='["docs-lint"]'; reviews='[]' ;;
  R1) mode=none; checks='["static","unit","build","code-quality"]'; reviews='["code-quality"]' ;;
  R2) mode=short; checks='["static","unit","integration","contract","e2e-short"]'; reviews='["specification","code-quality"]' ;;
  R3) mode=soak; checks='["static","unit","integration","contract","e2e-short","domain-failure-paths","e2e-soak","specification-review","code-quality-review"]'; reviews='["specification","code-quality"]' ;;
  R4) mode=release-soak; checks='["static","unit","integration","contract","e2e-short","domain-failure-paths","e2e-soak","specification-review","code-quality-review","release-soak","deployment","production-verification"]'; reviews='["specification","code-quality","release-evidence"]' ;;
  *) printf 'unsupported verification risk level: %s\n' "$risk" >&2; exit 2 ;;
esac

mkdir -p "$(dirname "$output")"
temporary=$output.tmp
jq -n --arg commit "$(git -C "$root" rev-parse HEAD)" \
  --arg manifest "sha256:$(sha256sum "$root/.test/verification.yaml" | cut -d' ' -f1)" \
  --arg risk "$risk" --arg mode "$mode" --argjson checks "$checks" --argjson reviews "$reviews" \
  '{version:1,commit:$commit,manifest_digest:$manifest,risk_level:$risk,mode:$mode,checks:$checks,reviews:$reviews}' >"$temporary"
mv "$temporary" "$output"
go run "$root/cmd/verification-schema" --schema "$root/.test/schemas/verification-plan.schema.json" --input "$output"
printf '%s\n' "$output"
