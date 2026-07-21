#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHECKER="${ROOT}/scripts/quality/risk-regression-guard.sh"
EXECUTOR="${ROOT}/scripts/quality/testdata/fake-risk-guard-executor.sh"
AGENT_INSTRUCTIONS="${ROOT}/docs/agent/instructions.md"
AGENT_INSTRUCTIONS_GENERATOR="${ROOT}/scripts/quality/generate-agent-instructions.sh"
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

strip_markdown_html_comments() {
  local input="$1" output="$2"
  awk '
    {
      rest = $0
      visible = ""
      while (length(rest) > 0) {
        if (in_comment) {
          comment_end = index(rest, "-->")
          if (comment_end == 0) {
            rest = ""
            break
          }
          rest = substr(rest, comment_end + 3)
          in_comment = 0
          continue
        }
        comment_start = index(rest, "<!--")
        if (comment_start == 0) {
          visible = visible rest
          rest = ""
          break
        }
        visible = visible substr(rest, 1, comment_start - 1)
        rest = substr(rest, comment_start + 4)
        in_comment = 1
      }
      print visible
    }
    END {
      if (in_comment) {
        print "unterminated Markdown HTML comment" > "/dev/stderr"
        exit 1
      }
    }
  ' "${input}" >"${output}"
}

validate_risk_harness_section() {
  local file="$1" fixture_name="$2" heading_count section visible principle
  heading_count="$(grep -Fxc '## Risk regression harness' "${file}" || true)"
  if [[ "${heading_count}" -ne 1 ]]; then
    echo "expected exactly one risk regression harness heading, found ${heading_count}" >&2
    return 1
  fi
  section="${TEST_ROOT}/${fixture_name}-risk-section.md"
  visible="${TEST_ROOT}/${fixture_name}-risk-visible.md"
  awk '
    /^## Risk regression harness$/ { printing = 1 }
    printing && !/^## Risk regression harness$/ && /^## / { exit }
    printing { print }
  ' "${file}" >"${section}"
  strip_markdown_html_comments "${section}" "${visible}" || return 1
  for principle in 'fail closed' 'bearer credential' 'tenant-scoped' \
    '破坏性' '持久化失败' '关闭旧资源' '真实链路验证' 'make risk-guardrails'; do
    if ! grep -Fq "${principle}" "${visible}"; then
      echo "risk regression harness section missing principle: ${principle}" >&2
      return 1
    fi
  done
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

validate_risk_harness_section "${AGENT_INSTRUCTIONS}" canonical

risk_duplicate="${TEST_ROOT}/risk-duplicate.md"
cat >"${risk_duplicate}" <<'EOF'
## Risk regression harness
fail closed bearer credential tenant-scoped 破坏性 持久化失败 关闭旧资源 真实链路验证 make risk-guardrails
## Risk regression harness
duplicate
EOF
if validate_risk_harness_section "${risk_duplicate}" duplicate >/dev/null 2>&1; then
  echo 'risk harness validation accepted duplicate headings' >&2
  exit 1
fi

risk_outside="${TEST_ROOT}/risk-outside.md"
cat >"${risk_outside}" <<'EOF'
## Risk regression harness
fail closed bearer credential tenant-scoped 破坏性 持久化失败 关闭旧资源 make risk-guardrails
## Other section
真实链路验证
EOF
if validate_risk_harness_section "${risk_outside}" outside >/dev/null 2>&1; then
  echo 'risk harness validation accepted a phrase outside its section' >&2
  exit 1
fi

risk_commented="${TEST_ROOT}/risk-commented.md"
cat >"${risk_commented}" <<'EOF'
## Risk regression harness
fail closed tenant-scoped 破坏性 持久化失败 关闭旧资源 真实链路验证 make risk-guardrails
<!-- bearer credential
hidden continuation -->
EOF
if validate_risk_harness_section "${risk_commented}" commented >/dev/null 2>&1; then
  echo 'risk harness validation accepted a phrase inside an HTML comment' >&2
  exit 1
fi

risk_unterminated="${TEST_ROOT}/risk-unterminated.md"
cat >"${risk_unterminated}" <<'EOF'
## Risk regression harness
fail closed bearer credential tenant-scoped 破坏性 持久化失败 关闭旧资源 真实链路验证 make risk-guardrails
<!-- unfinished
EOF
if validate_risk_harness_section "${risk_unterminated}" unterminated >/dev/null 2>&1; then
  echo 'risk harness validation accepted an unterminated HTML comment' >&2
  exit 1
fi
/bin/bash "${AGENT_INSTRUCTIONS_GENERATOR}" --check

explanation="$(/bin/bash "${CHECKER}" --explain)"
for principle in 'fail closed' 'bearer credential' 'tenant-scoped' \
  '破坏性' '持久化失败' '关闭旧资源' '真实链路验证'; do
  if ! grep -q "${principle}" <<< "${explanation}"; then
    echo "risk guard explanation missing principle: ${principle}" >&2
    exit 1
  fi
done
if ! grep -q 'make risk-guardrails' <<< "${explanation}"; then
  echo 'risk guard explanation does not expose make risk-guardrails' >&2
  exit 1
fi

echo 'risk regression guard tests passed'
