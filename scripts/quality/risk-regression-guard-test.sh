#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHECKER="${ROOT}/scripts/quality/risk-regression-guard.sh"
EXECUTOR="${ROOT}/scripts/quality/testdata/fake-risk-guard-executor.sh"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf "${TEST_ROOT}"' EXIT

run_guard() {
  local name="$1"
  shift
  local log="${TEST_ROOT}/${name}.log"
  local output="${TEST_ROOT}/${name}.out"
  : > "${log}"
  RISK_GUARD_COMMAND_LOG="${log}" RISK_GUARD_EXECUTOR="${EXECUTOR}" \
    /bin/bash "${CHECKER}" "$@" > "${output}"
  printf '%s' "${log}"
}

assert_label() {
  local log="$1" label="$2"
  if ! grep -q "^${label}|" "${log}"; then
    echo "missing risk guard label ${label} in ${log}" >&2
    exit 1
  fi
}

assert_label_once() {
  local log="$1" label="$2" count
  count="$(grep -c "^${label}|" "${log}" || true)"
  if [[ "${count}" -ne 1 ]]; then
    echo "risk guard label ${label} ran ${count} times, want 1" >&2
    exit 1
  fi
}

assert_file_contains() {
  local file="$1" pattern="$2" description="$3"
  if ! grep -Eq "${pattern}" "${file}"; then
    echo "missing ${description} in ${file}" >&2
    exit 1
  fi
}

unrelated_log="$(run_guard unrelated docs/readme.md)"
if [[ -s "${unrelated_log}" ]]; then
  echo 'unrelated files triggered risk guard commands' >&2
  exit 1
fi

routes_log="$(run_guard routes \
  api/wiring/platform.go \
  pkg/storage/postgres/tenant_schema.sql \
  .github/workflows/deploy.yml \
  api/http/handler/auth_handler.go \
  internal/knowledge/application/ingest_service.go \
  internal/memory/application/memory_service.go \
  internal/mcp/infrastructure/client.go \
  api/middleware/rate_limit.go \
  web/src/modules/iam/pages/auth/CallbackPage.tsx \
  web/package-lock.json)"

for label in architecture migration deployment auth-http knowledge memory mcp \
  runtime-governance frontend-auth frontend-supply-chain; do
  assert_label "${routes_log}" "${label}"
done

dedupe_log="$(run_guard dedupe api/http/router.go api/http/handler/auth_handler.go)"
assert_label_once "${dedupe_log}" auth-http

all_log="$(run_guard all --all)"
for label in architecture migration deployment auth-http knowledge memory mcp \
  runtime-governance frontend-auth frontend-supply-chain; do
  assert_label_once "${all_log}" "${label}"
done

failure_log="${TEST_ROOT}/failure.log"
: > "${failure_log}"
set +e
RISK_GUARD_COMMAND_LOG="${failure_log}" RISK_GUARD_EXECUTOR="${EXECUTOR}" \
  RISK_GUARD_FAIL_LABEL=auth-http /bin/bash "${CHECKER}" api/http/handler/auth_handler.go
status=$?
set -e
if [[ "${status}" -ne 42 ]]; then
  echo "risk guard returned ${status}, want propagated status 42" >&2
  exit 1
fi

assert_file_contains "${ROOT}/.pre-commit-config.yaml" \
  'id:[[:space:]]*risk-regression-guard' 'pre-commit risk guard hook'
assert_file_contains "${ROOT}/.pre-commit-config.yaml" \
  'entry:[[:space:]]*bash scripts/quality/risk-regression-guard\.sh' 'pre-commit risk guard entry'
assert_file_contains "${ROOT}/.pre-commit-config.yaml" \
  'require_serial:[[:space:]]*true' 'serialized pre-commit risk guard'
assert_file_contains "${ROOT}/.github/workflows/ci.yml" \
  'risk-regression-guard-test\.sh' 'CI risk guard self-test'
assert_file_contains "${ROOT}/.github/workflows/ci.yml" \
  'risk-regression-guard\.sh --all' 'CI full risk guard'
assert_file_contains "${ROOT}/.github/workflows/ci.yml" \
  'actions/setup-node@' 'CI Node setup for full risk guard'
assert_file_contains "${ROOT}/Makefile" '^risk-guardrails:' 'Makefile risk guard target'

echo 'risk regression guard tests passed'
