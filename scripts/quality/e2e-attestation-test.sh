#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$root"

if grep -Fq 'system-e2e-attestation' .github/workflows/ci.yml; then
  printf 'primary CI still references the removed system-e2e-attestation job\n' >&2
  exit 1
fi

grep -Fq 'PolicyManifestPath' internal/platform/e2eattestation/attestation.go
grep -Fq -- '--bundle' scripts/quality/test-verification-report.sh
grep -Fq -- '--signer-workflow' scripts/quality/test-verification-report.sh
grep -Fq -- '--source-digest' scripts/quality/test-verification-report.sh
grep -Fq -- '--source-ref' scripts/quality/test-verification-report.sh
grep -Fq 'https://token.actions.githubusercontent.com' scripts/quality/test-verification-report.sh
grep -Fq '.tested_git_parent == $commit' scripts/quality/test-verification-report.sh
if grep -Fq 'GITHUB_ACTIONS' scripts/quality/test-verification-report.sh; then
  printf 'completion report still trusts the caller-declared GitHub Actions environment\n' >&2
  exit 1
fi
if grep -Eq 'TEST_VERIFY_(SPEC|QUALITY|RELEASE)_REVIEW' scripts/quality/test-verification-report.sh; then
  printf 'verification workflow still accepts self-declared review status\n' >&2
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
bash scripts/quality/test-verification-report-test.sh
printf 'E2E attestation guard tests passed\n'
