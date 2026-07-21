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
  block="$(awk '
    /^repos:$/ { repos++; in_repos = 1; next }
    /^[^ #]/ && !/^repos:$/ { in_repos = 0; in_local = 0; in_hooks = 0 }
    in_repos && /^  - repo: local$/ { local_repos++; in_local = 1; in_hooks = 0; next }
    in_repos && /^  - repo:/ && !/^  - repo: local$/ { in_local = 0; in_hooks = 0 }
    in_local && /^    hooks:$/ { hooks++; in_hooks = 1; next }
    in_hooks && /^    [^ ]/ && !/^    hooks:$/ { in_hooks = 0 }
    in_hooks && /^      - id: agent-instructions-check$/ {
      targets++
      if (targets == 1) printing = 1
    }
    printing && !/^      - id: agent-instructions-check$/ && /^      - id:/ { printing = 0 }
    printing && /^[^ ]|^  [^ ]|^    [^ ]/ { printing = 0 }
    printing { block = block $0 ORS }
    END {
      if (repos != 1 || local_repos != 1 || hooks != 1 || targets != 1) {
        print "invalid repos/local/hooks hierarchy or target hook count" > "/dev/stderr"
        exit 1
      }
      printf "%s", block
    }
  ' "${file}")" || return 1
  assert_block_field "${block}" name \
    '^        name: agent instructions are generated and current$' 'exact hook name' || return 1
  assert_block_field "${block}" language '^        language: system$' \
    'system hook language' || return 1
  assert_block_field "${block}" entry \
    '^        entry: /bin/bash scripts/quality/generate-agent-instructions\.sh --check$' \
    'exact check-only hook entry' || return 1
  assert_block_field "${block}" pass_filenames '^        pass_filenames: false$' \
    'disabled filename passing' || return 1
  assert_block_field "${block}" require_serial '^        require_serial: true$' \
    'serialized hook' || return 1
  [[ "$(grep -Ec '^        files:' <<<"${block}" || true)" -eq 1 ]] || {
    echo 'expected exactly one files regex in bounded pre-commit hook' >&2
    return 1
  }
  files_regex="$(sed -nE "s/^        files: '([^']+)'$/\1/p" <<<"${block}")"
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
  local file="$1" block
  block="$(awk '
    /^jobs:$/ { jobs++; in_jobs = 1; next }
    /^[^ #]/ && !/^jobs:$/ { in_jobs = 0; in_guardrails = 0; in_steps = 0 }
    in_jobs && /^  guardrails:$/ { guardrails++; in_guardrails = 1; in_steps = 0; next }
    in_jobs && /^  [^ ]/ && !/^  guardrails:$/ { in_guardrails = 0; in_steps = 0 }
    in_guardrails && /^    steps:$/ { steps++; in_steps = 1; next }
    in_steps && /^    [^ ]/ && !/^    steps:$/ { in_steps = 0 }
    in_steps && /^      - uses: actions\/checkout@v4$/ { checkout = NR }
    in_steps && /^      - uses: actions\/setup-go@/ { setup_go = NR }
    in_steps && /^      - uses: actions\/setup-node@/ { setup_node = NR }
    in_steps && /^      - name: Verify generated agent instructions$/ {
      targets++
      target_line = NR
      if (targets == 1) printing = 1
    }
    printing && !/^      - name: Verify generated agent instructions$/ && /^      - (name|uses):/ {
      printing = 0
    }
    printing && /^[^ ]|^  [^ ]|^    [^ ]/ { printing = 0 }
    printing { block = block $0 ORS }
    END {
      if (jobs != 1 || guardrails != 1 || steps != 1 || targets != 1 ||
          !checkout || !setup_go || !setup_node || target_line <= checkout ||
          target_line >= setup_go || target_line >= setup_node) {
        print "invalid jobs/guardrails/steps hierarchy or step ordering" > "/dev/stderr"
        exit 1
      }
      printf "%s", block
    }
  ' "${file}")" || return 1
  assert_block_field "${block}" run '^        run: make agent-instructions-check$' \
    'exact CI check command' || return 1
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

wrong_parent_precommit="${TEST_ROOT}/wrong-parent-pre-commit.yaml"
cat >"${wrong_parent_precommit}" <<'EOF'
repos:
  - repo: local
    hooks:
      - id: unrelated
outside_repos:
      - id: agent-instructions-check
        name: agent instructions are generated and current
        language: system
        entry: /bin/bash scripts/quality/generate-agent-instructions.sh --check
        pass_filenames: false
        require_serial: true
        files: '^(AGENTS\.md|CLAUDE\.md|docs/agent/instructions\.md|docs/agent/templates/(agents-prefix|claude-prefix)\.md|scripts/quality/generate-agent-instructions(-test)?\.sh)$'
EOF
if validate_precommit_integration "${wrong_parent_precommit}" >/dev/null 2>&1; then
  fail 'pre-commit validation accepted a complete hook outside repos[].hooks'
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

wrong_parent_ci="${TEST_ROOT}/wrong-parent-ci.yml"
cat >"${wrong_parent_ci}" <<'EOF'
jobs:
  guardrails:
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
      - uses: actions/setup-node@v4
  lint:
    steps:
      - name: Verify generated agent instructions
        run: make agent-instructions-check
EOF
if validate_ci_integration "${wrong_parent_ci}" >/dev/null 2>&1; then
  fail 'CI validation accepted a complete step under another job'
fi

echo 'agent instruction generator tests passed'
