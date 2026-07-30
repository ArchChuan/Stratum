#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ANALYZER="${ROOT}/scripts/quality/code-quality-ratchet.go"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf "${TEST_ROOT}"' EXIT

mkdir -p "${TEST_ROOT}/sample"

write_fixture() {
  local name="$1" body="$2"
  printf 'package sample\n\n%s\n' "${body}" >"${TEST_ROOT}/sample/${name}.go"
}

scan() {
  go run "${ANALYZER}" scan --root "${TEST_ROOT}" "$@"
}

ratchet() {
  local mode="$1"
  shift
  go run "${ANALYZER}" "${mode}" --root "${TEST_ROOT}" "$@"
}

assert_metric() {
  local output="$1" pattern="$2" description="$3"
  if ! grep -Eq "${pattern}" <<<"${output}"; then
    echo "missing ${description}: ${pattern}" >&2
    echo "${output}" >&2
    exit 1
  fi
}

write_fixture clean 'func Clean(v bool) int { if v { return 1 }; return 0 }'
clean_output="$(scan sample/clean.go)"
assert_metric "${clean_output}" '"cyclomatic": 2' 'clean cyclomatic metric'
assert_metric "${clean_output}" '"cognitive": 1' 'clean cognitive metric'

write_fixture branches 'func Branches(v int) int {
  if v == 1 || v == 2 { v++ }
  if v == 3 { v++ }
  switch v { case 4: v++; case 5: v++ }
  for v < 7 { v++ }
  return v
}'
branch_output="$(scan sample/branches.go)"
assert_metric "${branch_output}" '"cyclomatic": 7' 'branch cyclomatic metric'

write_fixture nesting 'func Nested(a, b, c bool) bool {
  if a { if b { if c { for a { return true } } } }
  return false
}'
nesting_output="$(scan sample/nesting.go)"
assert_metric "${nesting_output}" '"max_nesting": 4' 'maximum nesting metric'
assert_metric "${nesting_output}" '"cognitive": 10' 'cognitive nesting penalty'

write_fixture infinite 'func Infinite() { for { break } }'
infinite_output="$(scan sample/infinite.go)"
assert_metric "${infinite_output}" '"cyclomatic": 2' 'conditionless loop metric'

write_fixture selection 'func Select(a, b <-chan int) {
  select { case <-a: return; case <-b: return; default: return }
}'
selection_output="$(scan sample/selection.go)"
assert_metric "${selection_output}" '"cyclomatic": 4' 'select communication clauses'

write_fixture else_nesting 'func ElseNesting(a, b bool) bool {
  if a { return true } else { if b { return true } }
  return false
}'
else_output="$(scan sample/else_nesting.go)"
assert_metric "${else_output}" '"max_nesting": 2' 'else block nesting'

write_fixture closure 'func WithClosure() func(int) int {
  return func(v int) int {
    if v > 0 { v++ }; if v > 1 { v++ }; if v > 2 { v++ }; if v > 3 { v++ }
    if v > 4 { v++ }; if v > 5 { v++ }; if v > 6 { v++ }; if v > 7 { v++ }
    if v > 8 { v++ }; if v > 9 { v++ }; return v
  }
}'
closure_output="$(scan sample/closure.go)"
assert_metric "${closure_output}" 'sample/closure.go:WithClosure\$literal1' 'anonymous function identity'
assert_metric "${closure_output}" '"cyclomatic": 11' 'anonymous function complexity'

{
  printf 'package sample\n\nfunc Long() int {\n'
  for _ in $(seq 1 125); do printf '  // padding\n'; done
  printf '  return 1\n}\n'
} >"${TEST_ROOT}/sample/long.go"
long_output="$(scan sample/long.go)"
assert_metric "${long_output}" '"lines": 128' 'function length metric'

first="$(scan sample/branches.go sample/clean.go)"
second="$(scan sample/clean.go sample/branches.go)"
if [[ "${first}" != "${second}" ]]; then
  echo 'metric output is not deterministic' >&2
  exit 1
fi

printf 'package sample\nfunc Broken(\n' >"${TEST_ROOT}/sample/broken.go"
if scan sample/broken.go >"${TEST_ROOT}/broken.out" 2>&1; then
  echo 'analyzer accepted malformed Go source' >&2
  exit 1
fi

write_fixture debt 'func Debt(v int) int {
  if v > 0 { v++ }; if v > 1 { v++ }; if v > 2 { v++ }; if v > 3 { v++ }
  if v > 4 { v++ }; if v > 5 { v++ }; if v > 6 { v++ }; if v > 7 { v++ }
  if v > 8 { v++ }; if v > 9 { v++ }; return v
}'
baseline="${TEST_ROOT}/baseline.json"
ratchet refresh --baseline "${baseline}" sample/debt.go
ratchet check --baseline "${baseline}" sample/debt.go

write_fixture debt 'func Debt(v int) int {
  if v > 0 { v++ }; if v > 1 { v++ }; if v > 2 { v++ }; if v > 3 { v++ }
  if v > 4 { v++ }; if v > 5 { v++ }; if v > 6 { v++ }; if v > 7 { v++ }
  if v > 8 { v++ }; if v > 9 { v++ }; if v > 10 { v++ }; return v
}'
if ratchet check --baseline "${baseline}" sample/debt.go >"${TEST_ROOT}/worsened.out" 2>&1; then
  echo 'ratchet accepted worsened historical debt' >&2
  exit 1
fi
assert_metric "$(<"${TEST_ROOT}/worsened.out")" 'cyclomatic.*11.*12.*10' 'actionable worsened metric'

write_fixture debt 'func RenamedDebt(v int) int {
  if v > 0 { v++ }; if v > 1 { v++ }; if v > 2 { v++ }; if v > 3 { v++ }
  if v > 4 { v++ }; if v > 5 { v++ }; if v > 6 { v++ }; if v > 7 { v++ }
  if v > 8 { v++ }; if v > 9 { v++ }; return v
}'
if ratchet check --baseline "${baseline}" sample/debt.go >/dev/null 2>&1; then
  echo 'ratchet accepted renamed historical debt as a new violation' >&2
  exit 1
fi

write_fixture debt 'func Debt(v int) int {
  if v > 0 { v++ }; if v > 1 { v++ }; if v > 2 { v++ }; if v > 3 { v++ }
  if v > 4 { v++ }; if v > 5 { v++ }; if v > 6 { v++ }; if v > 7 { v++ }
  if v > 8 { v++ }; return v
}'
ratchet check --baseline "${baseline}" sample/debt.go

refresh_copy="${TEST_ROOT}/baseline-copy.json"
ratchet refresh --baseline "${refresh_copy}" sample/debt.go
cp "${refresh_copy}" "${TEST_ROOT}/baseline-first.json"
ratchet refresh --baseline "${refresh_copy}" sample/debt.go
cmp "${TEST_ROOT}/baseline-first.json" "${refresh_copy}"

printf '{broken' >"${TEST_ROOT}/malformed.json"
if ratchet check --baseline "${TEST_ROOT}/malformed.json" sample/debt.go >/dev/null 2>&1; then
  echo 'ratchet accepted malformed baseline' >&2
  exit 1
fi

cp "${baseline}" "${TEST_ROOT}/trailing.json"
printf 'garbage' >>"${TEST_ROOT}/trailing.json"
if ratchet check --baseline "${TEST_ROOT}/trailing.json" sample/debt.go >/dev/null 2>&1; then
  echo 'ratchet accepted trailing baseline garbage' >&2
  exit 1
fi

wrapper_output="$(bash "${ROOT}/scripts/quality/code-quality-ratchet.sh" docs/agent/instructions.md)"
assert_metric "${wrapper_output}" 'no tracked production Go changes' 'wrapper non-Go exclusion'
wrapper_output="$(bash "${ROOT}/scripts/quality/code-quality-ratchet.sh" internal/workflow/domain/workflow_test.go)"
assert_metric "${wrapper_output}" 'no tracked production Go changes' 'wrapper test exclusion'

git_root="${TEST_ROOT}/git-wrapper"
mkdir -p "${git_root}/scripts/quality" "${git_root}/internal/sample"
cp "${ANALYZER}" "${ROOT}/scripts/quality/code-quality-ratchet.sh" "${git_root}/scripts/quality/"
git -C "${git_root}" init -q
git -C "${git_root}" config user.email quality-ratchet@example.test
git -C "${git_root}" config user.name quality-ratchet
printf 'package sample\nfunc Existing(v int) int { return v }\n' >"${git_root}/internal/sample/sample.go"
printf 'package sample\nfunc TestOnly() {}\n' >"${git_root}/internal/sample/sample_test.go"
git -C "${git_root}" add .
git -C "${git_root}" commit -qm baseline
CODE_QUALITY_ROOT="${git_root}" bash "${git_root}/scripts/quality/code-quality-ratchet.sh" --refresh-baseline
printf 'package sample\nfunc Existing(v int) int {\n' >"${git_root}/internal/sample/sample.go"
for _ in $(seq 1 11); do
  printf 'if v > 0 { v++ };\n' >>"${git_root}/internal/sample/sample.go"
done
printf 'return v\n}\n' >>"${git_root}/internal/sample/sample.go"
git -C "${git_root}" add internal/sample/sample.go
if CODE_QUALITY_ROOT="${git_root}" bash "${git_root}/scripts/quality/code-quality-ratchet.sh" \
  >"${TEST_ROOT}/staged-wrapper.out" 2>&1; then
  echo 'wrapper accepted a staged new complexity violation' >&2
  exit 1
fi
assert_metric "$(<"${TEST_ROOT}/staged-wrapper.out")" 'cyclomatic.*target=10' 'staged wrapper violation'

git -C "${git_root}" restore --staged internal/sample/sample.go
git -C "${git_root}" restore internal/sample/sample.go
printf 'package sample\nfunc Untracked(v int) int { if v > 0 { return v }; return 0 }\n' \
  >"${git_root}/internal/sample/untracked.go"
wrapper_output="$(CODE_QUALITY_ROOT="${git_root}" bash "${git_root}/scripts/quality/code-quality-ratchet.sh" \
  internal/sample/untracked.go)"
assert_metric "${wrapper_output}" 'no tracked production Go changes' 'wrapper untracked exclusion'

rm "${git_root}/internal/sample/untracked.go"
printf 'package sample\nfunc Debt(v int) int {\n' >"${git_root}/internal/sample/debt.go"
for _ in $(seq 1 10); do
  printf 'if v > 0 { v++ };\n' >>"${git_root}/internal/sample/debt.go"
done
printf 'return v\n}\n' >>"${git_root}/internal/sample/debt.go"
git -C "${git_root}" add internal/sample/debt.go
git -C "${git_root}" commit -qm debt
CODE_QUALITY_ROOT="${git_root}" bash "${git_root}/scripts/quality/code-quality-ratchet.sh" --refresh-baseline
git -C "${git_root}" mv internal/sample/debt.go internal/sample/renamed.go
if CODE_QUALITY_ROOT="${git_root}" bash "${git_root}/scripts/quality/code-quality-ratchet.sh" \
  >"${TEST_ROOT}/rename-wrapper.out" 2>&1; then
  echo 'wrapper accepted renamed historical debt' >&2
  exit 1
fi
git -C "${git_root}" reset -q HEAD internal/sample/debt.go internal/sample/renamed.go
git -C "${git_root}" restore internal/sample/debt.go
rm -f "${git_root}/internal/sample/renamed.go"
git -C "${git_root}" rm -q internal/sample/debt.go
wrapper_output="$(CODE_QUALITY_ROOT="${git_root}" bash "${git_root}/scripts/quality/code-quality-ratchet.sh")"
assert_metric "${wrapper_output}" 'no tracked production Go changes' 'wrapper deletion handling'

echo 'code quality ratchet tests passed'
