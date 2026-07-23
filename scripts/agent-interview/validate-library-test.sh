#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VALIDATOR="${ROOT}/scripts/agent-interview/validate-library.sh"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf "${TEST_ROOT}"' EXIT

readonly CATEGORY_FILES=(
  01-agent-runtime-and-workflow.md
  02-tools-mcp-and-approval.md
  03-context-and-memory.md
  04-knowledge-and-rag.md
  05-llm-gateway-and-model-routing.md
  06-reliability-and-streaming.md
  07-evaluation-observability-and-cost.md
  08-security-iam-and-multitenancy.md
  09-architecture-and-production-readiness.md
  99-unclassified.md
)

fail() {
  echo "$*" >&2
  exit 1
}

write_category() {
  local path="$1" id="$2"
  cat >"${path}" <<EOF
# Test Category

## 分类边界

测试边界。

## 趋势与观点

### T-${id}-trend 测试趋势

测试趋势正文。

## 面试题

### Q-${id}-question 测试问题

- 作答要点：测试答案。
- 深挖问题：测试追问。
- Stratum 实现与边界：测试边界。
- 相关源码/文档：\`docs/agent/agent.md\`
- 来源：SRC-test
- 首次收录：2026-07-09
- 最近更新：2026-07-23

## Stratum 可补强点

### G-${id}-gap 测试补强点

测试建议。

## 跟踪关键词

- test keyword

## 参考来源

### SRC-test

- URL: https://example.com/test
- 标题: Test Source
- 类型: official
- 首次收录: 2026-07-09
- 最近核验: 2026-07-23
EOF
}

build_valid_library() {
  local library="$1" file id
  mkdir -p "${library}/inbox"
  cat >"${library}/README.md" <<'EOF'
# Agent 高级开发面试资料索引

## 固定分类

测试分类契约。

## 分类优先级

测试分类优先级。

## 稳定 ID 与去重规则

测试 ID 规则。

## 已处理报告

| run_id | report_date | sha256 | input_count | created | updated | duplicate | unclassified |
|---|---|---|---:|---:|---:|---:|---:|
| 20260723-084409 | 2026-07-23 | 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef | 1 | 1 | 0 | 0 | 0 |

## 融合状态

- 最近成功融合：2026-07-23T08:44:09+08:00
- 原始条目数：1
- 稳定条目数：10
- 重复条目数：0
- 待分类条目数：0
EOF

  for file in "${CATEGORY_FILES[@]}"; do
    id="${file%%-*}"
    id="${id#0}"
    [[ "${file}" == 99-* ]] && id=99
    write_category "${library}/${file}" "${id}"
  done
  rm -f "${library}/99-unclassified.md"
  cat >"${library}/99-unclassified.md" <<'EOF'
# 待分类

## 分类边界

只承载不能唯一分类的条目。

## 趋势与观点

当前无。

## 面试题

当前无。

## Stratum 可补强点

当前无。

## 跟踪关键词

当前无。

## 参考来源

当前无。
EOF
  ln -s README.md "${library}/latest.md"
}

assert_rejected() {
  local name="$1" expected="$2" library
  library="${TEST_ROOT}/${name}"
  shift 2
  build_valid_library "${library}"
  "$@" "${library}"
  if "${VALIDATOR}" --library "${library}" >"${TEST_ROOT}/${name}.out" 2>&1; then
    fail "validator accepted invalid fixture: ${name}"
  fi
  grep -Fq "${expected}" "${TEST_ROOT}/${name}.out" || {
    cat "${TEST_ROOT}/${name}.out" >&2
    fail "validator did not report expected error for ${name}: ${expected}"
  }
}

add_extra_file() { printf '# extra\n' >"$1/10-extra.md"; }
remove_heading() { sed -i '/^## 参考来源$/d' "$1/01-agent-runtime-and-workflow.md"; }
duplicate_id() {
  sed -i 's/Q-2-question/Q-1-question/' "$1/02-tools-mcp-and-approval.md"
}
break_unclassified_count() { sed -i 's/待分类条目数：0/待分类条目数：1/' "$1/README.md"; }

valid_library="${TEST_ROOT}/valid"
build_valid_library "${valid_library}"
"${VALIDATOR}" --library "${valid_library}"

assert_rejected extra-file 'unexpected Markdown file' add_extra_file
assert_rejected missing-heading 'missing required heading' remove_heading
assert_rejected duplicate-id 'duplicate stable ID' duplicate_id
assert_rejected unclassified-count 'unclassified count mismatch' break_unclassified_count

coverage="${TEST_ROOT}/coverage.tsv"
printf '20260723-084409.md|20260723-084409:Q1|Q-1-question\n' >"${coverage}"
"${VALIDATOR}" --library "${valid_library}" --coverage-manifest "${coverage}"
printf '20260723-084409.md|20260723-084409:Q2|Q-missing\n' >"${coverage}"
if "${VALIDATOR}" --library "${valid_library}" --coverage-manifest "${coverage}" \
  >"${TEST_ROOT}/coverage.out" 2>&1; then
  fail 'validator accepted missing coverage target'
fi
grep -Fq 'coverage references unknown stable ID' "${TEST_ROOT}/coverage.out" || {
  cat "${TEST_ROOT}/coverage.out" >&2
  fail 'validator did not report unknown coverage stable ID'
}

echo 'agent interview library validator tests passed'
