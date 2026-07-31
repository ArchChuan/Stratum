#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
ci="$root/.github/workflows/stateful-e2e.yml"
cd "$root"

grep -Fq 'name: Stateful Browser E2E' "$ci"
grep -Fq 'make test-verify-attestation' "$ci"
grep -Fq 'make test-verify-report' "$ci"
grep -Fq 'STATEFUL_E2E_PROFILE: test' "$ci"
grep -Fq 'STATEFUL_E2E_PACKS: all' "$ci"
grep -Fq 'make e2e-system-soak' "$ci"
grep -Fq 'specification-review' "$ci"
grep -Fq 'code-quality-review' "$ci"
grep -Fq 'completion-report.json' "$ci"

empty_attestations=$(mktemp -d)
trap 'rm -rf "$empty_attestations"' EXIT
set +e
missing_output=$(make e2e-attestation-check E2E_ATTESTATION_DIR="$empty_attestations" 2>&1)
missing_status=$?
set -e
if ((missing_status == 0)); then
  printf 'e2e-attestation-check accepted a missing current-source attestation\n' >&2
  exit 1
fi
if ! grep -Fq 'missing current source attestation' <<<"$missing_output"; then
  printf 'e2e-attestation-check did not explain the missing current-source attestation\n' >&2
  exit 1
fi

go test ./internal/platform/e2eattestation ./cmd/e2e-attestation -run 'Attestation|AcceptanceProfile|RunRejects' -count=1
printf 'E2E attestation guard tests passed\n'
