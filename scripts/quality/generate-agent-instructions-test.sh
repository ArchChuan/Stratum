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

echo 'agent instruction generator tests passed'
