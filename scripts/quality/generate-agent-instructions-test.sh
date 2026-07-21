#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GENERATOR="${ROOT}/scripts/quality/generate-agent-instructions.sh"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf "${TEST_ROOT}"' EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  local haystack="$1"
  local needle="$2"
  [[ "${haystack}" == *"${needle}"* ]] || fail "expected output to contain: ${needle}"
}

assert_not_contains() {
  local haystack="$1"
  local needle="$2"
  [[ "${haystack}" != *"${needle}"* ]] || fail "expected output not to contain: ${needle}"
}

extract_unique_block() {
  local file="$1" start_pattern="$2" end_pattern="$3" description="$4" count
  count="$(grep -Ec "${start_pattern}" "${file}" || true)"
  if [[ "${count}" -ne 1 ]]; then
    echo "expected exactly one ${description} in ${file}, found ${count}" >&2
    return 1
  fi
  awk -v start="${start_pattern}" -v end="${end_pattern}" '
    $0 ~ start { printing = 1 }
    printing && $0 !~ start && $0 ~ end { exit }
    printing { print }
  ' "${file}"
}

assert_block_field() {
  local block="$1" key="$2" pattern="$3" description="$4" count
  count="$(grep -Ec "^[[:space:]]*${key}:" <<<"${block}" || true)"
  if [[ "${count}" -ne 1 ]] || ! grep -Eq "${pattern}" <<<"${block}"; then
    echo "expected exactly one ${description} in bounded YAML block" >&2
    return 1
  fi
}

validate_precommit_integration() {
  local file="$1" block files_regex required_path
  block="$(extract_unique_block "${file}" \
    '^[[:space:]]*-[[:space:]]+id:[[:space:]]+agent-instructions-check[[:space:]]*$' \
    '^[[:space:]]*-[[:space:]]+id:' 'agent-instructions-check hook')" || return 1
  assert_block_field "${block}" name \
    '^[[:space:]]*name:[[:space:]]+agent instructions are generated and current$' 'exact hook name' || return 1
  assert_block_field "${block}" language '^[[:space:]]*language:[[:space:]]+system$' \
    'system hook language' || return 1
  assert_block_field "${block}" entry \
    '^[[:space:]]*entry:[[:space:]]+/bin/bash scripts/quality/generate-agent-instructions\.sh --check$' \
    'exact check-only hook entry' || return 1
  assert_block_field "${block}" pass_filenames '^[[:space:]]*pass_filenames:[[:space:]]+false$' \
    'disabled filename passing' || return 1
  assert_block_field "${block}" require_serial '^[[:space:]]*require_serial:[[:space:]]+true$' \
    'serialized hook' || return 1
  [[ "$(grep -Ec '^[[:space:]]*files:' <<<"${block}" || true)" -eq 1 ]] || {
    echo 'expected exactly one files regex in bounded pre-commit hook' >&2
    return 1
  }
  files_regex="$(sed -nE "s/^[[:space:]]*files:[[:space:]]*'([^']+)'[[:space:]]*$/\1/p" <<<"${block}")"
  [[ -n "${files_regex}" ]] || {
    echo 'missing single-quoted files regex in bounded pre-commit hook' >&2
    return 1
  }
  for required_path in AGENTS.md CLAUDE.md docs/agent/instructions.md \
    docs/agent/templates/agents-prefix.md docs/agent/templates/claude-prefix.md \
    scripts/quality/generate-agent-instructions.sh scripts/quality/generate-agent-instructions-test.sh; do
    if [[ ! "${required_path}" =~ ${files_regex} ]]; then
      echo "pre-commit files regex does not cover ${required_path}" >&2
      return 1
    fi
  done
}

validate_ci_integration() {
  local file="$1" block checkout_line step_line setup_go_line setup_node_line
  block="$(extract_unique_block "${file}" \
    '^[[:space:]]*-[[:space:]]+name:[[:space:]]+Verify generated agent instructions[[:space:]]*$' \
    '^[[:space:]]*-[[:space:]]+(name|uses):' 'generated instruction CI step')" || return 1
  assert_block_field "${block}" run '^[[:space:]]*run:[[:space:]]+make agent-instructions-check$' \
    'exact CI check command' || return 1
  checkout_line="$(grep -nE '^[[:space:]]*-[[:space:]]+uses:[[:space:]]+actions/checkout@' "${file}" | head -1 | cut -d: -f1)"
  step_line="$(grep -nE \
    '^[[:space:]]*-[[:space:]]+name:[[:space:]]+Verify generated agent instructions[[:space:]]*$' \
    "${file}" | cut -d: -f1)"
  setup_go_line="$(grep -nE '^[[:space:]]*-[[:space:]]+uses:[[:space:]]+actions/setup-go@' "${file}" | head -1 | cut -d: -f1)"
  setup_node_line="$(grep -nE '^[[:space:]]*-[[:space:]]+uses:[[:space:]]+actions/setup-node@' "${file}" | head -1 | cut -d: -f1)"
  if [[ -z "${checkout_line}" || -z "${setup_go_line}" || -z "${setup_node_line}" ]] ||
    (( step_line <= checkout_line || step_line >= setup_go_line || step_line >= setup_node_line )); then
    echo 'generated instruction CI step must be after checkout and before Go/Node setup' >&2
    return 1
  fi
}

new_fixture() {
  local name="$1"
  FIXTURE="${TEST_ROOT}/${name}"
  mkdir -p "${FIXTURE}/docs/agent/templates" "${FIXTURE}/scripts/quality"
  cp "${GENERATOR}" "${FIXTURE}/scripts/quality/generate-agent-instructions.sh"
  cat >"${FIXTURE}/docs/agent/instructions.md" <<'EOF'
# Agent instructions

shared-rule
EOF
  cat >"${FIXTURE}/docs/agent/templates/agents-prefix.md" <<'EOF'
> Codex entry

codex-only
EOF
  cat >"${FIXTURE}/docs/agent/templates/claude-prefix.md" <<'EOF'
> Claude Code entry

claude-only
EOF
}

[[ -f "${GENERATOR}" ]] || fail "generator not implemented: ${GENERATOR}"

new_fixture default

for invalid_args in 'invalid' '--check extra'; do
  read -r -a args <<<"${invalid_args}"
  set +e
  invalid_output="$(cd "${FIXTURE}" && /bin/bash scripts/quality/generate-agent-instructions.sh "${args[@]}" 2>&1)"
  invalid_status=$?
  set -e
  [[ "${invalid_status}" -eq 2 ]] || fail "invalid arguments exited ${invalid_status}, expected 2: ${invalid_args}"
  assert_contains "${invalid_output}" 'usage: generate-agent-instructions.sh [--check]'
done

(
  cd "${FIXTURE}"
  /bin/bash scripts/quality/generate-agent-instructions.sh
)

[[ -f "${FIXTURE}/AGENTS.md" ]] || fail 'default generation did not create AGENTS.md'
[[ -f "${FIXTURE}/CLAUDE.md" ]] || fail 'default generation did not create CLAUDE.md'
agents_content="$(<"${FIXTURE}/AGENTS.md")"
claude_content="$(<"${FIXTURE}/CLAUDE.md")"
assert_contains "${agents_content}" 'generated; do not edit directly'
assert_contains "${claude_content}" 'generated; do not edit directly'
assert_contains "${agents_content}" 'shared-rule'
assert_contains "${claude_content}" 'shared-rule'
assert_contains "${agents_content}" 'codex-only'
assert_not_contains "${agents_content}" 'claude-only'
assert_contains "${claude_content}" 'claude-only'
assert_not_contains "${claude_content}" 'codex-only'
assert_not_contains "${agents_content}" $'codex-only\n\n\n---'
assert_not_contains "${claude_content}" $'claude-only\n\n\n---'

agents_mtime="$(stat -c %Y "${FIXTURE}/AGENTS.md")"
claude_mtime="$(stat -c %Y "${FIXTURE}/CLAUDE.md")"
sleep 1
(
  cd "${FIXTURE}"
  /bin/bash scripts/quality/generate-agent-instructions.sh
)
[[ "$(stat -c %Y "${FIXTURE}/AGENTS.md")" == "${agents_mtime}" ]] || fail 'unchanged AGENTS.md mtime changed'
[[ "$(stat -c %Y "${FIXTURE}/CLAUDE.md")" == "${claude_mtime}" ]] || fail 'unchanged CLAUDE.md mtime changed'

(
  cd "${FIXTURE}"
  /bin/bash scripts/quality/generate-agent-instructions.sh --check
) || fail '--check failed for current generated files'

echo 'changed-shared-rule' >>"${FIXTURE}/docs/agent/instructions.md"
if check_output="$(cd "${FIXTURE}" && /bin/bash scripts/quality/generate-agent-instructions.sh --check 2>&1)"; then
  fail '--check succeeded after common instructions changed'
fi
assert_contains "${check_output}" 'AGENTS.md'
assert_contains "${check_output}" 'CLAUDE.md'

(
  cd "${FIXTURE}"
  /bin/bash scripts/quality/generate-agent-instructions.sh
)
echo 'changed-codex-only' >>"${FIXTURE}/docs/agent/templates/agents-prefix.md"
if check_output="$(cd "${FIXTURE}" && /bin/bash scripts/quality/generate-agent-instructions.sh --check 2>&1)"; then
  fail '--check succeeded after agents prefix changed'
fi
assert_contains "${check_output}" 'AGENTS.md'
assert_not_contains "${check_output}" 'CLAUDE.md'

(
  cd "${FIXTURE}/docs/agent"
  /bin/bash ../../scripts/quality/generate-agent-instructions.sh
  /bin/bash ../../scripts/quality/generate-agent-instructions.sh --check
) || fail 'generation from docs/agent did not produce current files'

new_fixture missing-claude-prefix
printf '%s\n' 'old-agents' >"${FIXTURE}/AGENTS.md"
printf '%s\n' 'old-claude' >"${FIXTURE}/CLAUDE.md"
rm "${FIXTURE}/docs/agent/templates/claude-prefix.md"
if missing_output="$(cd "${FIXTURE}" && /bin/bash scripts/quality/generate-agent-instructions.sh 2>&1)"; then
  fail 'generation succeeded with missing claude prefix'
fi
[[ "$(<"${FIXTURE}/AGENTS.md")" == 'old-agents' ]] || fail 'failed generation changed AGENTS.md'
[[ "$(<"${FIXTURE}/CLAUDE.md")" == 'old-claude' ]] || fail 'failed generation changed CLAUDE.md'

new_fixture extra-prefix-blank-line
printf '%s\n' 'old-agents' >"${FIXTURE}/AGENTS.md"
printf '%s\n' 'old-claude' >"${FIXTURE}/CLAUDE.md"
printf '\n' >>"${FIXTURE}/docs/agent/templates/agents-prefix.md"
if extra_blank_output="$(cd "${FIXTURE}" && /bin/bash scripts/quality/generate-agent-instructions.sh 2>&1)"; then
  fail 'generation succeeded with an extra trailing prefix blank line'
fi
assert_contains "${extra_blank_output}" \
  'agent instructions: prefix must not end with a blank line: docs/agent/templates/agents-prefix.md'
[[ "$(<"${FIXTURE}/AGENTS.md")" == 'old-agents' ]] || fail 'invalid prefix changed AGENTS.md'
[[ "$(<"${FIXTURE}/CLAUDE.md")" == 'old-claude' ]] || fail 'invalid prefix changed CLAUDE.md'

new_fixture comparison-error
printf '%s\n' 'old-agents' >"${FIXTURE}/AGENTS.md"
printf '%s\n' 'old-claude' >"${FIXTURE}/CLAUDE.md"
cat >"${FIXTURE}/cmp-error" <<'EOF'
#!/usr/bin/env bash
exit 2
EOF
chmod +x "${FIXTURE}/cmp-error"
if comparison_output="$(
  cd "${FIXTURE}" &&
    AGENT_INSTRUCTIONS_CMP_BIN="${FIXTURE}/cmp-error" /bin/bash scripts/quality/generate-agent-instructions.sh 2>&1
)"; then
  fail 'generation succeeded after comparison failed'
fi
assert_contains "${comparison_output}" 'comparison failed for AGENTS.md'
assert_not_contains "${comparison_output}" 'stale generated entry'
[[ "$(<"${FIXTURE}/AGENTS.md")" == 'old-agents' ]] || fail 'comparison failure changed AGENTS.md'
[[ "$(<"${FIXTURE}/CLAUDE.md")" == 'old-claude' ]] || fail 'comparison failure changed CLAUDE.md'

new_fixture rollback
printf '%s\n' 'old-agents' >"${FIXTURE}/AGENTS.md"
printf '%s\n' 'old-claude' >"${FIXTURE}/CLAUDE.md"
cat >"${FIXTURE}/move-with-second-failure" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
count=0
[[ ! -f "${MOVE_COUNT_FILE}" ]] || count="$(<"${MOVE_COUNT_FILE}")"
count=$((count + 1))
printf '%s\n' "${count}" >"${MOVE_COUNT_FILE}"
if (( count == 2 )); then
  exit 1
fi
exec /bin/mv "$@"
EOF
chmod +x "${FIXTURE}/move-with-second-failure"
if rollback_output="$(
  cd "${FIXTURE}" &&
    MOVE_COUNT_FILE="${FIXTURE}/move-count" \
      AGENT_INSTRUCTIONS_MOVE_BIN="${FIXTURE}/move-with-second-failure" \
      /bin/bash scripts/quality/generate-agent-instructions.sh 2>&1
)"; then
  fail 'generation succeeded after second install failed'
fi
assert_contains "${rollback_output}" 'failed to install CLAUDE.md'
assert_not_contains "${rollback_output}" 'rollback was incomplete'
[[ "$(<"${FIXTURE}/AGENTS.md")" == 'old-agents' ]] || fail 'rollback did not restore AGENTS.md'
[[ "$(<"${FIXTURE}/CLAUDE.md")" == 'old-claude' ]] || fail 'failed install changed CLAUDE.md'
[[ "$(<"${FIXTURE}/move-count")" == '3' ]] || fail 'rollback did not use the expected move sequence'
if find "${FIXTURE}" -maxdepth 1 -name '.*.install.*' -print -quit | grep -q .; then
  fail 'failed install left a temporary install file'
fi

(
  cd "${ROOT}"
  /bin/bash "${GENERATOR}" --check
) || fail 'repository generated instructions are not current'
git -C "${ROOT}" ls-files --error-unmatch AGENTS.md >/dev/null || fail 'AGENTS.md is not tracked'
git -C "${ROOT}" ls-files --error-unmatch CLAUDE.md >/dev/null || fail 'CLAUDE.md is not tracked'
if ignore_output="$(git -C "${ROOT}" check-ignore --no-index -v AGENTS.md 2>&1)"; then
  fail "AGENTS.md is ignored: ${ignore_output}"
fi
if ignore_output="$(git -C "${ROOT}" check-ignore --no-index -v CLAUDE.md 2>&1)"; then
  fail "CLAUDE.md is ignored: ${ignore_output}"
fi

[[ "$(grep -c '^agent-instructions:$' "${ROOT}/Makefile" || true)" -eq 1 ]] || \
  fail 'Makefile must contain exactly one agent-instructions target'
[[ "$(grep -c '^agent-instructions-check:$' "${ROOT}/Makefile" || true)" -eq 1 ]] || \
  fail 'Makefile must contain exactly one agent-instructions-check target'
validate_precommit_integration "${ROOT}/.pre-commit-config.yaml" || fail 'invalid pre-commit integration'
validate_ci_integration "${ROOT}/.github/workflows/ci.yml" || fail 'invalid CI integration'

bad_precommit="${TEST_ROOT}/bad-pre-commit.yaml"
sed 's#entry: /bin/bash scripts/quality/generate-agent-instructions.sh --check#entry: /bin/bash wrong.sh --check#' \
  "${ROOT}/.pre-commit-config.yaml" >"${bad_precommit}"
printf '%s\n' '# entry: /bin/bash scripts/quality/generate-agent-instructions.sh --check' >>"${bad_precommit}"
if validate_precommit_integration "${bad_precommit}" >/dev/null 2>&1; then
  fail 'bounded pre-commit validation accepted a correct entry only in a comment'
fi

bad_ci="${TEST_ROOT}/bad-ci.yml"
awk '
  /name: Verify generated agent instructions/ { in_step = 1 }
  in_step && /run: make agent-instructions-check/ { sub(/make agent-instructions-check/, "echo skipped") }
  { print }
  END { print "      # run: make agent-instructions-check" }
' "${ROOT}/.github/workflows/ci.yml" >"${bad_ci}"
if validate_ci_integration "${bad_ci}" >/dev/null 2>&1; then
  fail 'bounded CI validation accepted a correct command only outside its step'
fi

echo 'agent instruction generator tests passed'
