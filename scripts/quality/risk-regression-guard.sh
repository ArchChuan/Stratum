#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

labels=(
  architecture
  migration
  deployment
  auth-http
  knowledge
  memory
  mcp
  runtime-governance
  frontend-auth
  frontend-supply-chain
)
declare -A selected=()

select_all() {
  local label
  for label in "${labels[@]}"; do
    selected["${label}"]=1
  done
}

select_for_path() {
  local path="$1"
  case "${path}" in
    api/wiring/*|.golangci.yml)
      selected[architecture]=1
      ;;
  esac
  case "${path}" in
    internal/migration/*|pkg/migration/*|pkg/storage/postgres/*schema*|scripts/quality/check-migration-boundaries*)
      selected[migration]=1
      ;;
  esac
  case "${path}" in
    helm/*|k8s/*|.github/workflows/deploy.yml|scripts/quality/check-deployment-safety*)
      selected[deployment]=1
      ;;
  esac
  case "${path}" in
    api/http/*|internal/iam/*)
      selected[auth-http]=1
      ;;
  esac
  case "${path}" in
    internal/knowledge/*|pkg/storage/milvus/*|pkg/vector/*)
      selected[knowledge]=1
      ;;
  esac
  case "${path}" in
    internal/memory/*)
      selected[memory]=1
      ;;
  esac
  case "${path}" in
    internal/mcp/*)
      selected[mcp]=1
      ;;
  esac
  case "${path}" in
    api/middleware/*|cmd/server/*)
      selected[runtime-governance]=1
      ;;
  esac
  case "${path}" in
    web/src/modules/iam/*)
      selected[frontend-auth]=1
      ;;
  esac
  case "${path}" in
    web/package.json|web/package-lock.json)
      selected[frontend-supply-chain]=1
      ;;
  esac
}

run_check() {
  local label="$1"
  shift
  printf 'risk regression guard: %s\n' "${label}"
  if [[ -n "${RISK_GUARD_EXECUTOR:-}" ]]; then
    /bin/bash "${RISK_GUARD_EXECUTOR}" "${label}" "$@"
    return
  fi
  "$@"
}

if [[ "${1:-}" == "--all" ]]; then
  if [[ "$#" -ne 1 ]]; then
    echo 'usage: risk-regression-guard.sh [--all | changed-file ...]' >&2
    exit 2
  fi
  select_all
elif [[ "$#" -gt 0 ]]; then
  for path in "$@"; do
    select_for_path "${path#./}"
  done
else
  while IFS= read -r path; do
    [[ -n "${path}" ]] && select_for_path "${path#./}"
  done < <(git diff --cached --name-only --diff-filter=ACMR)
fi

if [[ "${#selected[@]}" -eq 0 ]]; then
  echo 'risk regression guard: no relevant changes'
  exit 0
fi

for label in "${labels[@]}"; do
  [[ -n "${selected[${label}]:-}" ]] || continue
  case "${label}" in
    architecture)
      run_check "${label}" /bin/bash -c \
        'bash scripts/quality/arch-guard-test.sh && bash scripts/quality/arch-guard.sh api/wiring/*.go'
      ;;
    migration)
      run_check "${label}" /bin/bash -c \
        'bash scripts/quality/check-migration-boundaries-test.sh && bash scripts/quality/check-migration-boundaries.sh && go test ./pkg/storage/postgres ./pkg/tenantdb'
      ;;
    deployment)
      run_check "${label}" /bin/bash scripts/quality/check-deployment-safety-test.sh
      ;;
    auth-http)
      run_check "${label}" go test ./api/http/... ./internal/iam/...
      ;;
    knowledge)
      run_check "${label}" go test ./internal/knowledge/... ./pkg/storage/milvus
      ;;
    memory)
      run_check "${label}" go test ./internal/memory/...
      ;;
    mcp)
      run_check "${label}" go test ./internal/mcp/...
      ;;
    runtime-governance)
      run_check "${label}" go test ./api/middleware ./api/http ./cmd/server
      ;;
    frontend-auth)
      run_check "${label}" /bin/bash -c \
        'npm --prefix web run typecheck && stratum-verify frontend-test'
      ;;
    frontend-supply-chain)
      run_check "${label}" npm --prefix web audit --audit-level=high
      ;;
  esac
done

echo 'risk regression guard: passed'
