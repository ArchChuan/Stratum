#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCANNER="${ROOT}/scripts/quality/secret-scan.sh"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf "${TEST_ROOT}"' EXIT

REPO="${TEST_ROOT}/repo"
OUT="${REPO}/tmp/secret-scan"
FAKE_GITLEAKS="${ROOT}/scripts/quality/testdata/fake-gitleaks.sh"
SENTINEL='scanner-regression-sensitive-value'

mkdir -p "${REPO}/tracked" "${OUT}/reports"
git -C "${REPO}" init -q
git -C "${REPO}" config user.email test@example.invalid
git -C "${REPO}" config user.name test
printf 'safe content\n' > "${REPO}/tracked/input.txt"
printf 'tmp/\n.env\n' > "${REPO}/.gitignore"
git -C "${REPO}" add .gitignore tracked/input.txt
git -C "${REPO}" commit -qm initial
printf '{"Secret":"%s","RuleID":"old-report"}\n' "${SENTINEL}" > "${OUT}/reports/previous.json"
printf 'API_KEY=%s\n' "${SENTINEL}" > "${REPO}/.env"

for _ in 1 2; do
  FAKE_GITLEAKS_SENTINEL="${SENTINEL}" STRATUM_ROOT="${REPO}" SECRET_SCAN_OUT_DIR="${OUT}" \
    GITLEAKS_BIN="${FAKE_GITLEAKS}" \
    "${SCANNER}" > "${TEST_ROOT}/scanner-output.log"
done

if grep -R -q "${SENTINEL}" "${TEST_ROOT}/scanner-output.log" "${OUT}/logs"; then
  echo 'secret scanner exposed a raw secret value' >&2
  exit 1
fi
if find "${OUT}/reports" -type f ! -name previous.json -print0 | xargs -0 grep -q '"RuleID"'; then
  echo 'secret scanner ingested ignored local files or a previous report' >&2
  exit 1
fi

echo 'secret scanner regression tests passed'
