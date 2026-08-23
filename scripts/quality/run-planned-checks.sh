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
  "${gen_make_command[@]}" risk-guardrails code-quality
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
  "${gen_make_command[@]}" fe-lint fe-build
}

run_contract_checks() {
  "${gen_make_command[@]}" contract-test
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

# 生成物并发竞态防护:
# - code-quality/fe-lint/fe-build 的 make 目标都无条件重跑 proto-gen(buf generate),
#   并行组各自重跑会竞态写 api/http/dto/gen 与 web/src/services/gen。
# - 解法:先串行生成一次(改了 .proto 时保证最新),再让静态/构建/契约组的
#   make 全部带 `-o proto-gen`(GNU make:该目标视为 up-to-date、跳过 recipe),
#   从根上杜绝重复写。docs 组(agent-instructions-check)不依赖生成物,保持普通 make。
# - CI_OWNED 模式下 CI 兜底组全部跳过、无并发;串行 proto-gen 仅在实际要跑
#   go test/go build/contract 且未被 CI 兜底时才执行。
need_gen=false
if [[ "$tests" == true && "$ci_skip_tests" != true ]] ||
   [[ "$build" == true && "$ci_skip_build" != true ]] ||
   [[ "$contract" == true && "$ci_skip_contract" != true ]]; then
  need_gen=true
fi
gen_make_command=("$make_command")
if [[ "$need_gen" == true ]]; then
  "$make_command" proto-gen
  # 数组保存 make + `-o proto-gen`(GNU make:目标视为 up-to-date、跳过 recipe),
  # 不能拼成带空格的字符串,否则整串会被当做一个命令名。
  gen_make_command=("$make_command" -o proto-gen)
fi

# 五组并行运行:每组输出进独立日志,失败时统一回放(前 300 行),传播全部失败
# 而非首个;成功组静默(保持终端整洁),CI 兜底跳过显式暴露。
tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/stratum-planned-checks.XXXXXX")
trap 'rm -rf "$tmpdir"' EXIT

declare -a group_pids=() group_names=() group_codes=()
launch_group() {
  local name=$1 skip=$2
  shift 2
  if [[ "$skip" == true ]]; then
    printf 'skipped (CI-owned): %s\n' "$name" >&2
    return 0
  fi
  ( "$@" >"$tmpdir/$name.log" 2>&1 ) &
  group_pids+=("$!")
  group_names+=("$name")
}

[[ "$docs" != true ]] || launch_group docs-lint "$ci_skip_docs" run_docs_checks
[[ "$static" != true ]] || launch_group static "$ci_skip_static" run_static_checks
[[ "$tests" != true ]] || launch_group tests "$ci_skip_tests" run_go_tests
[[ "$build" != true ]] || launch_group build "$ci_skip_build" run_build_checks
[[ "$contract" != true ]] || launch_group contract "$ci_skip_contract" run_contract_checks

failed=0
for i in "${!group_pids[@]}"; do
  rc=0
  wait "${group_pids[$i]}" || rc=$?
  group_codes[$i]=$rc
  if ((rc == 0)); then
    printf 'ok: %s\n' "${group_names[$i]}"
  else
    printf 'failed: %s\n' "${group_names[$i]}" >&2
  fi
done

for i in "${!group_names[@]}"; do
  if ((group_codes[$i] != 0)); then
    failed=1
    printf '\n--- 失败组输出: %s ---\n' "${group_names[$i]}" >&2
    sed -n '1,300p' "$tmpdir/${group_names[$i]}.log" >&2
  fi
done

exit "$failed"
