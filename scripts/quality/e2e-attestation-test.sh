#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$root"

if grep -Fq 'system-e2e-attestation' .github/workflows/ci.yml; then
  printf 'primary CI still references the removed system-e2e-attestation job\n' >&2
  exit 1
fi

grep -Fq 'PolicyManifestPath' internal/platform/e2eattestation/attestation.go
grep -Fq '.tested_git_parent == $commit' scripts/quality/write-local-verification-report.sh
grep -Fq -- '--required-mode' scripts/quality/write-local-verification-report.sh
if grep -Eq 'Sigstore|github-actions-sigstore|TEST_VERIFY_(SIGNATURE|CHECK|REVIEW)' \
  scripts/quality/write-local-verification-report.sh; then
  printf 'local report still contains CI or signature authority\n' >&2
  exit 1
fi

grep -Fq 'kubectl get deployment' .github/workflows/deploy.yml
grep -Fq 'deployment-receipt.json' .github/workflows/deploy.yml

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

go test ./internal/platform/e2eattestation ./cmd/e2e-attestation \
  -run 'Attestation|AcceptanceProfile|RunRejects|RunTopology' -count=1
bash scripts/quality/local-verification-report-test.sh
printf 'E2E attestation guard tests passed\n'
