#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
plan=${TEST_VERIFY_PLAN_PATH:-$root/tmp/test-verification/plan.json}
make_command=${TEST_VERIFY_MAKE_COMMAND:-make}
go_command=${TEST_VERIFY_GO_COMMAND:-go}
clean_env=(env -u STRATUM_TEST_POSTGRES_URL -u TEST_DATABASE_URL -u POSTGRES_URL
  -u REDIS_URL -u NATS_URL -u MILVUS_HOST -u MILVUS_PORT)

run_docs_checks() {
  "$make_command" agent-instructions-check
}

run_static_checks() {
  "$make_command" risk-guardrails code-quality
  "${clean_env[@]}" "$go_command" vet ./...
}

run_go_tests() {
  local packages=()
  mapfile -t packages < <("$go_command" list ./... | grep -Ev '/test/e2e($|/)|/web/node_modules/')
  ((${#packages[@]} > 0)) || { printf 'no focused Go test packages selected\n' >&2; return 1; }
  "${clean_env[@]}" "$go_command" test -short "${packages[@]}"
}

run_build_checks() {
  "$go_command" build ./cmd/server
  "$make_command" fe-lint fe-build
}

run_contract_checks() {
  "$make_command" contract-test
}

[[ -f "$plan" ]] || { printf 'verification plan is missing: %s\n' "$plan" >&2; exit 1; }
docs=false
static=false
tests=false
build=false
contract=false
while IFS= read -r check; do
  case "$check" in
    docs-lint) docs=true ;;
    static | code-quality | domain-failure-paths) static=true ;;
    unit | integration) tests=true ;;
    build) build=true ;;
    contract) contract=true ;;
    e2e-short | e2e-soak | release-soak) ;;
    *) printf 'unsupported local verification check: %s\n' "$check" >&2; exit 1 ;;
  esac
done < <(jq -er '.local_checks[]' "$plan")

[[ "$docs" != true ]] || run_docs_checks
[[ "$static" != true ]] || run_static_checks
[[ "$tests" != true ]] || run_go_tests
[[ "$build" != true ]] || run_build_checks
[[ "$contract" != true ]] || run_contract_checks
