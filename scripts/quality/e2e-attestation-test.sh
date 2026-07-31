#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
ci="$root/.github/workflows/stateful-e2e.yml"
cd "$root"

if grep -Fq 'system-e2e-attestation' .github/workflows/ci.yml; then
  printf 'primary CI still references the removed system-e2e-attestation job\n' >&2
  exit 1
fi

grep -Fq 'name: Stateful Browser E2E' "$ci"
grep -Fq 'make test-verify-attestation' "$ci"
grep -Fq 'make test-verify-report' "$ci"
grep -Fq 'STATEFUL_E2E_PROFILE: test' "$ci"
grep -Fq 'STATEFUL_E2E_PACKS: all' "$ci"
grep -Fq 'make e2e-system-soak' "$ci"
grep -Fq 'specification-review' "$ci"
grep -Fq 'code-quality-review' "$ci"
grep -Fq 'completion-report.json' "$ci"
grep -Fq 'actions/attest@v4' "$ci"
grep -Fq 'gh attestation verify' "$ci"
grep -Fq 'environment: specification-review' "$ci"
grep -Fq 'environment: code-quality-review' "$ci"
grep -Fq 'TEST_VERIFY_SIGNATURE_RECEIPT' "$ci"
stateful_job=$(awk '
  /^  stateful-e2e:/ { capture=1 }
  capture && /^  [a-zA-Z0-9_-]+:/ && !/^  stateful-e2e:/ { exit }
  capture { print }
' "$ci")
grep -Fq 'fetch-depth: 0' <<<"$stateful_job"
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
if grep -Eq 'TEST_VERIFY_(SPEC|QUALITY|RELEASE)_REVIEW' scripts/quality/test-verification-report.sh "$ci"; then
  printf 'verification workflow still accepts self-declared review status\n' >&2
  exit 1
fi

release_ci="$root/.github/workflows/release-verification.yml"
grep -Fq 'make e2e-system-release-soak' "$release_ci"
grep -Fq 'environment: release-evidence-review' "$release_ci"
grep -Fq 'environment: production-verification' "$release_ci"
grep -Fq 'deployment-evidence' "$release_ci"
grep -Fq 'deployment-receipt.json' "$release_ci"
grep -Fq 'actions: read' "$release_ci"
grep -Fq 'gh run view' "$release_ci"
grep -Fq '.workflowName == "Build and Deploy"' "$release_ci"
grep -Fq '.conclusion == "success"' "$release_ci"
grep -Fq '.headSha == $sha' "$release_ci"
grep -Fq '.headBranch == "main"' "$release_ci"
grep -Fq -- '--signer-workflow' "$release_ci"
grep -Fq 'test "$GITHUB_REF" = refs/heads/main' "$release_ci"
grep -Fq 'kubectl get deployment' .github/workflows/deploy.yml
grep -Fq 'deployment-receipt.json' .github/workflows/deploy.yml
if grep -Fq 'inputs.artifact_digest' "$release_ci"; then
  printf 'release verification still accepts a caller-provided artifact digest\n' >&2
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

go test ./internal/platform/e2eattestation ./cmd/e2e-attestation \
  -run 'Attestation|AcceptanceProfile|RunRejects|RunTopology' -count=1
bash scripts/quality/test-verification-report-test.sh
printf 'E2E attestation guard tests passed\n'
