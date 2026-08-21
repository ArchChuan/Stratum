#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
plan=${TEST_VERIFY_PLAN_PATH:-$root/tmp/test-verification/plan.json}
make_command=${TEST_VERIFY_MAKE_COMMAND:-make}
go_command=${TEST_VERIFY_GO_COMMAND:-go}
go_parallelism=$(bash "$root/scripts/quality/go-parallelism.sh")
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
  "${clean_env[@]}" "$go_command" test -short -p "$go_parallelism" "${packages[@]}"
}

run_build_checks() {
  "$go_command" build -p "$go_parallelism" ./cmd/server
  "$make_command" fe-lint fe-build
}

run_contract_checks() {
  "$make_command" contract-test
}

[[ -f "$plan" ]] || { printf 'verification plan is missing: %s\n' "$plan" >&2; exit 1; }

# CI_OWNED=1：.test/verification.yaml 的 ci_checks 声明了哪些检查由 CI job 兜底
# （verification-ci-contract-test.sh 已保证每个标识符映射到 ci.yml 的真实 job）。
# 本地跳过重复项、只跑 CI 不覆盖的检查；跳过必须显式暴露，不得静默。
# plan 缺 ci_checks 声明时 fail closed（宁可全跑也不误跳过）。
ci_skip_docs=false
ci_skip_static=false
ci_skip_tests=false
ci_skip_build=false
ci_skip_contract=false
if [[ "${CI_OWNED:-0}" == "1" ]]; then
  jq -e '.ci_checks | type == "array"' "$plan" >/dev/null ||
    { printf 'CI_OWNED requires ci_checks in plan: %s\n' "$plan" >&2; exit 1; }
  while IFS= read -r check; do
    case "$check" in
      docs-lint) ci_skip_docs=true ;;
      static | code-quality | risk-guardrails) ci_skip_static=true ;;
      unit | integration) ci_skip_tests=true ;;
      build) ci_skip_build=true ;;
      contract) ci_skip_contract=true ;;
      security | e2e-short | e2e-soak | release-soak) ;;
      *) printf 'unsupported CI-owned check: %s\n' "$check" >&2; exit 1 ;;
    esac
  done < <(jq -er '.ci_checks[]' "$plan")
fi

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

run_unless_ci_owned() {
  local name=$1 skip=$2
  shift 2
  if [[ "$skip" == true ]]; then
    printf 'skipped (CI-owned): %s\n' "$name" >&2
    return 0
  fi
  "$@"
}

[[ "$docs" != true ]] || run_unless_ci_owned docs-lint "$ci_skip_docs" run_docs_checks
[[ "$static" != true ]] || run_unless_ci_owned static "$ci_skip_static" run_static_checks
[[ "$tests" != true ]] || run_unless_ci_owned tests "$ci_skip_tests" run_go_tests
[[ "$build" != true ]] || run_unless_ci_owned build "$ci_skip_build" run_build_checks
[[ "$contract" != true ]] || run_unless_ci_owned contract "$ci_skip_contract" run_contract_checks
