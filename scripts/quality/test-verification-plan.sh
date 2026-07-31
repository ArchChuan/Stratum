#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
output=${TEST_VERIFY_PLAN_PATH:-$root/tmp/test-verification/plan.json}
base_ref=${TEST_VERIFY_BASE_REF:-origin/main}
mkdir -p "$(dirname "$output")"
args=(--root "$root" --base-ref "$base_ref" --output "$output")
[[ -z "${TEST_VERIFY_RISK_LEVEL:-}" ]] || args+=(--minimum-risk "$TEST_VERIFY_RISK_LEVEL")
[[ "${TEST_VERIFY_RELEASE_INTENT:-false}" != true ]] || args+=(--release)
go run "$root/cmd/verification-plan" "${args[@]}"
go run "$root/cmd/verification-schema" --schema "$root/.test/schemas/verification-plan.schema.json" --input "$output"
printf '%s\n' "$output"
