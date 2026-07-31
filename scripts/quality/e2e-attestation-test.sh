#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
ci="$root/.github/workflows/ci.yml"
cd "$root"

grep -Fq 'name: System E2E Attestation' "$ci"
grep -Fq 'make e2e-attestation-check' "$ci"
grep -Fq 'E2E_REQUIRED_MODE=' "$ci"
grep -Fq 'E2E_REQUIRED_PROFILE=test' "$ci"
grep -Fq 'system-e2e-attestation' "$ci"

job=$(awk '
  /^  system-e2e-attestation:/ { capture=1 }
  capture && /^  [a-zA-Z0-9_-]+:/ && !/^  system-e2e-attestation:/ { exit }
  capture { print }
' "$ci")
if grep -Eq 'playwright install|Install Chromium|npm ci|go run ./cmd/server|docker compose' <<<"$job"; then
  printf 'System E2E Attestation CI job must validate only and never run browsers or services\n' >&2
  exit 1
fi

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
