#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/common.sh"
CODEX_START="$SCRIPT_DIR/codex-task-start.sh"
CODEX_STOP="$SCRIPT_DIR/codex-stop.sh"
CLAUDE_START="$SCRIPT_DIR/claude-task-start.sh"
CLAUDE_STOP="$SCRIPT_DIR/claude-stop.sh"
REPORT="$SCRIPT_DIR/report.sh"
INSTALLER="$SCRIPT_DIR/install-hooks.sh"
FIXTURES="$(mktemp -d)"
trap 'rm -rf "$FIXTURES"' EXIT

count=0
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
pass() { count=$((count + 1)); printf 'ok %d - %s\n' "$count" "$1"; }

new_repo() {
  local name="$1" repo
  repo="$FIXTURES/$name"
  mkdir -p "$repo/docs/agent"
  printf 'module github.com/ArchChuan/Stratum\n\ngo 1.25.12\n' >"$repo/go.mod"
  printf '# policy\n' >"$repo/docs/agent/knowledge-deposition.md"
  git -C "$repo" init -q
  git -C "$repo" config user.email test@example.com
  git -C "$repo" config user.name Test
  git -C "$repo" add .
  git -C "$repo" commit -qm initial
  printf '%s\n' "$repo"
}

payload() {
  jq -cn --arg cwd "$1" --arg session "$2" --arg event "$3" --argjson active "${4:-false}" \
    '{cwd:$cwd,session_id:$session,hook_event_name:$event,stop_hook_active:$active,prompt:"RAW-PROMPT-MUST-NOT-PERSIST"}'
}

run_hook() { printf '%s' "$2" | "$1"; }
run_installer() { timeout 10s "$INSTALLER" "$@"; }

valid_none() {
  jq -cn '{decision:"none",task_summary:"Routine maintenance only.",none_reason:"No reusable knowledge was produced.",candidates:[]}'
}

marker_value() { jq -er '.task_id' "$1/tmp/knowledge-deposition/current/$2-$3.json"; }

wait_for_file() {
  local path="$1" deadline=$((SECONDS + 5))
  while (( SECONDS < deadline )); do [[ -e "$path" ]] && return 0; sleep 0.02; done
  return 1
}

wait_for_exact_lock_fd() {
  local root_pid="$1" lock="$2" deadline=$((SECONDS + 5)) pid fd target index child children
  local -a processes
  while (( SECONDS < deadline )); do
    processes=("$root_pid")
    index=0
    while (( index < ${#processes[@]} )); do
      pid="${processes[$index]}"
      index=$((index + 1))
      children="$(cat "/proc/$pid/task/$pid/children" 2>/dev/null || true)"
      for child in $children; do processes+=("$child"); done
    done
    for pid in "${processes[@]}"; do
      [[ -d "/proc/$pid/fd" ]] || continue
      for fd in /proc/"$pid"/fd/*; do
        target="$(readlink -f "$fd" 2>/dev/null || true)"
        [[ "$target" == "$lock" ]] && return 0
      done
    done
    sleep 0.02
  done
  return 1
}

for required in "$CODEX_START" "$CODEX_STOP" "$CLAUDE_START" "$CLAUDE_STOP"; do
  [[ -x "$required" ]] || fail "required adapter missing or not executable: $required"
done
pass "all adapters exist"

repo="$(new_repo main)"
codex_raw_session='codex/session unsafe'
codex_out="$(run_hook "$CODEX_START" "$(payload "$repo" "$codex_raw_session" UserPromptSubmit)")"
jq -e '.continue == true and (.systemMessage | contains("Client: codex") and contains("Session:") and contains("Task:") and contains("bash scripts/knowledge-deposition/report.sh --client codex"))' \
  <<<"$codex_out" >/dev/null || fail "Codex start envelope mismatch"
codex_marker="$(find "$repo/tmp/knowledge-deposition/current" -name 'codex-*.json' -print -quit)"
[[ -f "$codex_marker" ]] || fail "Codex marker missing"
codex_session="$(jq -r '.session_id' "$codex_marker")"
codex_task="$(jq -r '.task_id' "$codex_marker")"
[[ "$codex_session" =~ ^[A-Za-z0-9._-]+$ ]] || fail "session was not sanitized"
[[ "$codex_task" =~ ^task-[0-9]+-[0-9a-f]{16}$ ]] || fail "task id format mismatch"
jq -e --arg root "$repo" '.schema_version == 1 and .client == "codex" and .repository.root == $root and (.created_at | type == "string")' \
  "$codex_marker" >/dev/null || fail "Codex marker schema mismatch"
if rg -l 'RAW-PROMPT-MUST-NOT-PERSIST' "$repo/tmp/knowledge-deposition" >/dev/null 2>&1; then fail "raw prompt persisted"; fi
pass "Codex task start creates a sanitized private marker and injection"

repo_quoted="$(new_repo 'repo space;touch-pwn')"
quoted_out="$(run_hook "$CODEX_START" "$(payload "$repo_quoted" quote-session UserPromptSubmit)")"
quoted_command="$(jq -r '.systemMessage | capture("(?<cmd>bash scripts/knowledge-deposition/report.sh .*)").cmd' <<<"$quoted_out")"
quoted_reason="$(run_hook "$CODEX_STOP" "$(payload "$repo_quoted" quote-session Stop false)")"
quoted_remediation="$(jq -r '.reason | capture("run: (?<cmd>bash scripts/knowledge-deposition/report.sh .*)").cmd' <<<"$quoted_reason")"
[[ "$quoted_remediation" == "$quoted_command" ]] || fail "remediation command quoting differs from injected command"
mkdir -p "$repo_quoted/scripts"
ln -s "$SCRIPT_DIR" "$repo_quoted/scripts/knowledge-deposition"
quoted_parent="$(dirname "$repo_quoted")"
rm -f "$quoted_parent/touch-pwn"
(cd "$repo_quoted" && printf '%s' "$(valid_none)" | bash -c "$quoted_command") >/dev/null || fail "quoted injected command was not executable"
[[ ! -e "$quoted_parent/touch-pwn" ]] || fail "repo-root metacharacters caused command injection"
pass "injected command shell-quotes repository roots with spaces and metacharacters"

repo_sessions="$(new_repo sessions)"
for raw in 'a/b' 'a?b' 'a_b'; do run_hook "$CODEX_START" "$(payload "$repo_sessions" "$raw" UserPromptSubmit)" >/dev/null; done
mapfile -t session_markers < <(find "$repo_sessions/tmp/knowledge-deposition/current" -name 'codex-*.json' -print | sort)
[[ "${#session_markers[@]}" -eq 3 ]] || fail "lossy session normalization collided"
mapfile -t session_keys < <(jq -r '.session_id' "${session_markers[@]}" | sort -u)
[[ "${#session_keys[@]}" -eq 3 ]] || fail "session keys are not distinct"
for key in "${session_keys[@]}"; do
  [[ "$key" =~ ^session-[0-9a-f]{64}$ ]] || fail "session key is not opaque SHA-256: $key"
done
if rg -l 'a/b|a\?b|"a_b"' "$repo_sessions/tmp/knowledge-deposition" >/dev/null 2>&1; then fail "raw session id persisted"; fi
pass "raw session identifiers map to distinct opaque deterministic keys"

claude_out="$(run_hook "$CLAUDE_START" "$(payload "$repo" claude-session UserPromptSubmit)")"
jq -e '.continue == true and .hookSpecificOutput.hookEventName == "UserPromptSubmit" and (.hookSpecificOutput.additionalContext | contains("Client: claude") and contains("bash scripts/knowledge-deposition/report.sh --client claude"))' \
  <<<"$claude_out" >/dev/null || fail "Claude start envelope mismatch"
pass "Claude task start preserves UserPromptSubmit protocol"

stop_payload="$(payload "$repo" "$codex_raw_session" Stop false)"
blocked="$(run_hook "$CODEX_STOP" "$stop_payload")"
jq -e --arg cmd "bash scripts/knowledge-deposition/report.sh --client codex --session $codex_session --task $codex_task --repo-root $repo" \
  '.decision == "block" and (.reason | contains("missing") and contains($cmd))' <<<"$blocked" >/dev/null || fail "missing report did not block actionably: $blocked"
pass "missing report blocks Codex stop"

printf '%s' "$(valid_none)" | "$REPORT" --client codex --session "$codex_session" --task "$codex_task" --repo-root "$repo" >/dev/null
allowed="$(run_hook "$CODEX_STOP" "$stop_payload")"
jq -e '.continue == true and .suppressOutput == true' <<<"$allowed" >/dev/null || fail "valid report did not allow quietly"
pass "valid report allows stop quietly"

report_json="$(find "$repo/tmp/knowledge-deposition" -mindepth 2 -name "codex-$codex_session-$codex_task.json" -print -quit)"
report_md="${report_json%.json}.md"
cp "$report_json" "$FIXTURES/report.json"
cp "$report_md" "$FIXTURES/report.md"

wrong_dir="$repo/tmp/knowledge-deposition/not-a-date"
mkdir -p "$wrong_dir"
mv "$report_json" "$report_md" "$wrong_dir/"
jq -e '.decision == "block"' <<<"$(run_hook "$CODEX_STOP" "$stop_payload")" >/dev/null || fail "non-date report directory allowed"
mv "$wrong_dir/$(basename "$report_json")" "$report_json"
mv "$wrong_dir/$(basename "$report_md")" "$report_md"
pass "report pair outside an authoritative date directory blocks"

wrong_date="$repo/tmp/knowledge-deposition/2000-01-01"
mkdir -p "$wrong_date"
mv "$report_json" "$report_md" "$wrong_date/"
jq -e '.decision == "block"' <<<"$(run_hook "$CODEX_STOP" "$stop_payload")" >/dev/null || fail "created_at date mismatch allowed"
mv "$wrong_date/$(basename "$report_json")" "$report_json"
mv "$wrong_date/$(basename "$report_md")" "$report_md"
pass "report directory date must match created_at UTC date"

printf '{bad\n' >"$report_json"
jq -e '.decision == "block"' <<<"$(run_hook "$CODEX_STOP" "$stop_payload")" >/dev/null || fail "corrupt report allowed"
cp "$FIXTURES/report.json" "$report_json"
pass "corrupt report JSON blocks"

rm "$report_md"
jq -e '.decision == "block" and (.reason | contains("Markdown"))' <<<"$(run_hook "$CODEX_STOP" "$stop_payload")" >/dev/null || fail "missing Markdown allowed"
cp "$FIXTURES/report.md" "$report_md"
pass "missing Markdown pair blocks"

printf '# stale but correctly named Markdown\n' >"$report_md"
jq -e '.decision == "block" and (.reason | contains("Markdown"))' <<<"$(run_hook "$CODEX_STOP" "$stop_payload")" >/dev/null || fail "stale Markdown content allowed"
cp "$FIXTURES/report.md" "$report_md"
pass "right-basename stale Markdown content blocks"

jq '.repository.commit = "0000000000000000000000000000000000000000"' "$FIXTURES/report.json" >"$report_json"
knowledge_render_markdown "$report_json" >"$report_md"
jq -e '.decision == "block" and (.reason | contains("commit"))' <<<"$(run_hook "$CODEX_STOP" "$stop_payload")" >/dev/null || fail "commit mismatch allowed"
cp "$FIXTURES/report.json" "$report_json"
pass "commit mismatch blocks"

cross_out="$(run_hook "$CODEX_START" "$(payload "$repo" cross-session UserPromptSubmit)")"
cross_task="$(jq -r '.systemMessage | capture("--task (?<v>[^ ]+) --repo-root").v' <<<"$cross_out")"
cross_key="$(jq -r '.systemMessage | capture("--session (?<v>[^ ]+) --task").v' <<<"$cross_out")"
cross_json="$(dirname "$report_json")/codex-$cross_key-$cross_task.json"
cp "$FIXTURES/report.json" "$cross_json"
cp "$FIXTURES/report.md" "${cross_json%.json}.md"
jq -e '.decision == "block" and (.reason | contains("identity"))' \
  <<<"$(run_hook "$CODEX_STOP" "$(payload "$repo" cross-session Stop false)")" >/dev/null || fail "cross-session report allowed"
pass "cross-session report identity blocks"

printf '%s' "$(payload "$repo" "$codex_raw_session" UserPromptSubmit)" | "$CODEX_START" >/dev/null
jq -e '.decision == "block"' <<<"$(run_hook "$CODEX_STOP" "$stop_payload")" >/dev/null || fail "previous task report allowed"
pass "advanced task marker rejects previous-task report"

active_payload="$(payload "$repo" "$codex_raw_session" Stop true)"
jq -e '.decision == "block"' <<<"$(run_hook "$CODEX_STOP" "$active_payload")" >/dev/null || fail "stop_hook_active bypassed gate"
pass "stop_hook_active does not bypass gate"

rm "$repo/tmp/knowledge-deposition/current/codex-$codex_session.json"
jq -e '.decision == "block" and (.reason | contains("marker"))' <<<"$(run_hook "$CODEX_STOP" "$stop_payload")" >/dev/null || fail "missing marker allowed"
pass "missing marker blocks"

missing_session="$(jq -cn --arg cwd "$repo" '{cwd:$cwd,hook_event_name:"Stop"}')"
jq -e '.decision == "block" and (.reason | contains("malformed hook payload"))' \
  <<<"$(run_hook "$CODEX_STOP" "$missing_session")" >/dev/null || fail "malformed Stratum payload allowed"
pass "malformed Stratum payload fails closed"

other="$FIXTURES/other"
mkdir -p "$other"
for adapter in "$CODEX_START" "$CODEX_STOP" "$CLAUDE_START" "$CLAUDE_STOP"; do
  out="$(run_hook "$adapter" "$(payload "$other" outside Stop)")"
  jq -e '.continue == true and .suppressOutput == true' <<<"$out" >/dev/null || fail "non-Stratum adapter was not quiet: $adapter"
done
pass "non-Stratum payloads quietly allow all adapters"

for adapter in "$CODEX_START" "$CODEX_STOP" "$CLAUDE_START" "$CLAUDE_STOP"; do
  out="$(printf '{bad' | "$adapter")"
  jq -e '.decision == "block" or (.continue == true and .suppressOutput == true)' <<<"$out" >/dev/null || fail "malformed payload response invalid"
done
pass "malformed payloads fail closed when repository scope is knowable"

failure_bin="$FIXTURES/failure-bin"
mkdir -p "$failure_bin"
for tool in date od jq mv sha256sum; do
  printf '#!/usr/bin/env bash\nexit 71\n' >"$failure_bin/$tool"
  chmod +x "$failure_bin/$tool"
  failure_out="$(PATH="$failure_bin:$PATH" run_hook "$CODEX_START" "$(payload "$repo" dependency-failure UserPromptSubmit)" 2>/dev/null || true)"
  jq -e '.decision == "block" and (.reason | type == "string")' <<<"$failure_out" >/dev/null || fail "$tool failure did not emit one valid block envelope: $failure_out"
  [[ "$failure_out" != *RAW-PROMPT-MUST-NOT-PERSIST* ]] || fail "$tool failure exposed raw hook input"
  rm "$failure_bin/$tool"
done
pass "task-start dependency failures emit valid block JSON"

stop_failure_repo="$(new_repo stop-failure)"
stop_failure_start="$(run_hook "$CODEX_START" "$(payload "$stop_failure_repo" stop-failure UserPromptSubmit)")"
stop_failure_key="$(jq -r '.systemMessage | capture("--session (?<v>[^ ]+) --task").v' <<<"$stop_failure_start")"
stop_failure_task="$(jq -r '.systemMessage | capture("--task (?<v>[^ ]+) --repo-root").v' <<<"$stop_failure_start")"
printf '%s' "$(valid_none)" | "$REPORT" --client codex --session "$stop_failure_key" --task "$stop_failure_task" --repo-root "$stop_failure_repo" >/dev/null
for tool in jq cmp; do
  printf '#!/usr/bin/env bash\nexit 72\n' >"$failure_bin/$tool"; chmod +x "$failure_bin/$tool"
  failure_out="$(PATH="$failure_bin:$PATH" run_hook "$CODEX_STOP" "$(payload "$stop_failure_repo" stop-failure Stop false)" 2>/dev/null || true)"
  jq -e '.decision == "block" and (.reason | type == "string")' <<<"$failure_out" >/dev/null || fail "Stop $tool failure did not emit one valid block envelope: $failure_out"
  [[ "$failure_out" != *RAW-PROMPT-MUST-NOT-PERSIST* ]] || fail "Stop $tool failure exposed raw hook input"
  rm "$failure_bin/$tool"
done
pass "Stop dependency failures emit valid block JSON"

repo_symlink="$(new_repo symlink)"
mkdir -p "$repo_symlink/tmp/knowledge-deposition"
outside_current="$FIXTURES/outside-current"
mkdir -p "$outside_current"
ln -s "$outside_current" "$repo_symlink/tmp/knowledge-deposition/current"
jq -e '.decision == "block"' <<<"$(run_hook "$CODEX_START" "$(payload "$repo_symlink" link-session UserPromptSubmit)")" >/dev/null || fail "current directory symlink accepted"
[[ -z "$(find "$outside_current" -type f -print -quit)" ]] || fail "symlink start wrote outside repo"
pass "symlink current directory is rejected"

repo_marker_link="$(new_repo marker-link)"
mkdir -p "$repo_marker_link/tmp/knowledge-deposition/current"
target="$FIXTURES/marker-target"
printf 'unchanged\n' >"$target"
marker_link_key="$(knowledge_normalize_session marker-session)"
ln -s "$target" "$repo_marker_link/tmp/knowledge-deposition/current/codex-$marker_link_key.json"
jq -e '.decision == "block"' <<<"$(run_hook "$CODEX_START" "$(payload "$repo_marker_link" marker-session UserPromptSubmit)")" >/dev/null || fail "marker symlink accepted"
[[ "$(cat "$target")" == unchanged ]] || fail "marker symlink target modified"
pass "marker symlink is rejected"

repo_unique="$(new_repo unique)"
first="$(run_hook "$CODEX_START" "$(payload "$repo_unique" unique UserPromptSubmit)")"
first_task="$(jq -r '.systemMessage | capture("--task (?<v>[^ ]+) --repo-root").v' <<<"$first")"
second="$(run_hook "$CODEX_START" "$(payload "$repo_unique" unique UserPromptSubmit)")"
second_task="$(jq -r '.systemMessage | capture("--task (?<v>[^ ]+) --repo-root").v' <<<"$second")"
[[ "$first_task" != "$second_task" ]] || fail "task ids collided"
pass "task ids are unique"

repo_concurrent="$(new_repo concurrent)"
pids=()
for i in $(seq 1 12); do
  run_hook "$CODEX_START" "$(payload "$repo_concurrent" same-session UserPromptSubmit)" >"$FIXTURES/start-$i.out" &
  pids+=("$!")
done
for pid in "${pids[@]}"; do wait "$pid" || fail "concurrent start failed"; done
marker="$(find "$repo_concurrent/tmp/knowledge-deposition/current" -name 'codex-*.json' -print -quit)"
jq -e '.schema_version == 1 and .client == "codex" and (.session_id | test("^session-[0-9a-f]{64}$")) and (.task_id | test("^task-[0-9]+-[0-9a-f]{16}$"))' "$marker" >/dev/null || fail "concurrent marker incomplete"
pass "concurrent starts leave one complete valid marker"

repo_race="$(new_repo check-race)"
race_start="$(run_hook "$CODEX_START" "$(payload "$repo_race" race-session UserPromptSubmit)")"
race_task="$(jq -r '.systemMessage | capture("--task (?<v>[^ ]+) --repo-root").v' <<<"$race_start")"
race_key="$(jq -r '.systemMessage | capture("--session (?<v>[^ ]+) --task").v' <<<"$race_start")"
printf '%s' "$(valid_none)" | "$REPORT" --client codex --session "$race_key" --task "$race_task" --repo-root "$repo_race" >/dev/null
race_lock="$repo_race/tmp/knowledge-deposition/.lock/report.lock"
race_marker="$repo_race/tmp/knowledge-deposition/current/codex-$race_key.json"
race_bin="$FIXTURES/race-bin"
race_ready="$FIXTURES/race.ready"
race_continue="$FIXTURES/race.continue"
real_mv="$(command -v mv)"
mkdir -p "$race_bin"
printf '%s\n' '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'destination="${!#}"' \
  'if [[ "$destination" == "$KD_TEST_MARKER" ]]; then' \
  '  ready_tmp="$KD_TEST_READY.$$"' \
  '  printf "ready\n" >"$ready_tmp"' \
  '  "$KD_TEST_REAL_MV" "$ready_tmp" "$KD_TEST_READY"' \
  '  deadline=$((SECONDS + 5))' \
  '  while [[ ! -e "$KD_TEST_CONTINUE" && $SECONDS -lt $deadline ]]; do sleep 0.02; done' \
  '  [[ -e "$KD_TEST_CONTINUE" ]] || exit 74' \
  'fi' \
  'exec "$KD_TEST_REAL_MV" "$@"' >"$race_bin/mv"
chmod +x "$race_bin/mv"
PATH="$race_bin:$PATH" KD_TEST_MARKER="$race_marker" KD_TEST_READY="$race_ready" \
  KD_TEST_CONTINUE="$race_continue" KD_TEST_REAL_MV="$real_mv" \
  run_hook "$CODEX_START" "$(payload "$repo_race" race-session UserPromptSubmit)" >"$FIXTURES/race-start.out" &
race_start_pid=$!
wait_for_file "$race_ready" || {
  kill "$race_start_pid" 2>/dev/null || true
  fail "task-start did not reach the marker replacement handshake"
}
run_hook "$CODEX_STOP" "$(payload "$repo_race" race-session Stop false)" >"$FIXTURES/race-check.out" &
race_check_pid=$!
wait_for_exact_lock_fd "$race_check_pid" "$race_lock" || {
  kill "$race_start_pid" "$race_check_pid" 2>/dev/null || true
  : >"$race_continue"
  fail "checker did not open the exact shared lock while task-start held it"
}
kill -0 "$race_check_pid" 2>/dev/null || fail "checker completed before marker replacement continued"
[[ ! -s "$FIXTURES/race-check.out" ]] || fail "checker emitted a result while task-start held the lock"
: >"$race_continue"
for pid in "$race_start_pid" "$race_check_pid"; do
  for _ in $(seq 1 100); do kill -0 "$pid" 2>/dev/null || break; sleep 0.02; done
  kill -0 "$pid" 2>/dev/null && { kill "$pid" 2>/dev/null || true; fail "ordered lock contender exceeded bounded wait"; }
  wait "$pid" || fail "ordered lock contender failed"
done
jq -e '.decision == "block"' "$FIXTURES/race-check.out" >/dev/null || fail "checker approved stale task during marker advance"
pass "mv handshake proves checker waits for marker replacement under the shared lock"

claude_repo="$(new_repo claude-stop)"
claude_start="$(run_hook "$CLAUDE_START" "$(payload "$claude_repo" claude-stop-session UserPromptSubmit)")"
claude_task="$(jq -r '.hookSpecificOutput.additionalContext | capture("--task (?<v>[^ ]+) --repo-root").v' <<<"$claude_start")"
claude_key="$(jq -r '.hookSpecificOutput.additionalContext | capture("--session (?<v>[^ ]+) --task").v' <<<"$claude_start")"
claude_payload="$(payload "$claude_repo" claude-stop-session Stop false)"
jq -e '.decision == "block" and (.reason | contains("missing"))' <<<"$(run_hook "$CLAUDE_STOP" "$claude_payload")" >/dev/null || fail "Claude missing report allowed"
jq -e '.decision == "block"' <<<"$(run_hook "$CLAUDE_STOP" "$(payload "$claude_repo" claude-stop-session Stop true)")" >/dev/null || fail "Claude stop_hook_active bypassed"
printf '%s' "$(valid_none)" | "$REPORT" --client claude --session "$claude_key" --task "$claude_task" --repo-root "$claude_repo" >/dev/null
claude_json="$(find "$claude_repo/tmp/knowledge-deposition" -mindepth 2 -name "claude-$claude_key-$claude_task.json" -print -quit)"
claude_md="${claude_json%.json}.md"
cp "$claude_json" "$FIXTURES/claude.json"; cp "$claude_md" "$FIXTURES/claude.md"
jq -e '.continue == true and .suppressOutput == true' <<<"$(run_hook "$CLAUDE_STOP" "$claude_payload")" >/dev/null || fail "Claude valid report blocked"
printf '{bad\n' >"$claude_json"
jq -e '.decision == "block"' <<<"$(run_hook "$CLAUDE_STOP" "$claude_payload")" >/dev/null || fail "Claude corrupt report allowed"
cp "$FIXTURES/claude.json" "$claude_json"; rm "$claude_md"
jq -e '.decision == "block"' <<<"$(run_hook "$CLAUDE_STOP" "$claude_payload")" >/dev/null || fail "Claude missing Markdown allowed"
cp "$FIXTURES/claude.md" "$claude_md"
jq '.repository.commit="0000000000000000000000000000000000000000"' "$FIXTURES/claude.json" >"$claude_json"
knowledge_render_markdown "$claude_json" >"$claude_md"
jq -e '.decision == "block"' <<<"$(run_hook "$CLAUDE_STOP" "$claude_payload")" >/dev/null || fail "Claude commit mismatch allowed"
cp "$FIXTURES/claude.json" "$claude_json"
run_hook "$CLAUDE_START" "$(payload "$claude_repo" claude-stop-session UserPromptSubmit)" >/dev/null
jq -e '.decision == "block"' <<<"$(run_hook "$CLAUDE_STOP" "$claude_payload")" >/dev/null || fail "Claude previous task report allowed"
cross_claude="$(run_hook "$CLAUDE_START" "$(payload "$claude_repo" claude-cross UserPromptSubmit)")"
cross_claude_task="$(jq -r '.hookSpecificOutput.additionalContext | capture("--task (?<v>[^ ]+) --repo-root").v' <<<"$cross_claude")"
cross_claude_key="$(jq -r '.hookSpecificOutput.additionalContext | capture("--session (?<v>[^ ]+) --task").v' <<<"$cross_claude")"
cross_claude_json="$(dirname "$claude_json")/claude-$cross_claude_key-$cross_claude_task.json"
cp "$FIXTURES/claude.json" "$cross_claude_json"; cp "$FIXTURES/claude.md" "${cross_claude_json%.json}.md"
jq -e '.decision == "block"' <<<"$(run_hook "$CLAUDE_STOP" "$(payload "$claude_repo" claude-cross Stop false)")" >/dev/null || fail "Claude cross-session report allowed"
pass "Claude Stop mirrors all lifecycle gates and envelopes"

installer_repo="$(new_repo installer)"
mkdir -p "$installer_repo/scripts"
ln -s "$SCRIPT_DIR" "$installer_repo/scripts/knowledge-deposition"
installer_configs="$FIXTURES/installer-configs"
mkdir -p "$installer_configs/codex" "$installer_configs/claude"
codex_config="$installer_configs/codex/hooks.json"
claude_config="$installer_configs/claude/settings.json"
jq -n '{theme:"dark",hooks:{UserPromptSubmit:[{matcher:"keep-metadata"},{matcher:"existing",hooks:[{type:"command",command:"echo codex-start"},{type:"command",command:"bash /old/repo/scripts/knowledge-deposition/codex-task-start.sh"},{type:"command",command:"echo /prior/scripts/knowledge-deposition/codex-task-start.sh"},{type:"command",command:"bash /prior/scripts/knowledge-deposition/codex-task-start.sh --extra"}]}],Stop:[{hooks:[{type:"command",command:"echo codex-stop"},{type:"command",command:"bash /old/repo\\ space/scripts/knowledge-deposition/codex-stop.sh"},{type:"command",command:"bash /prior;echo/scripts/knowledge-deposition/codex-stop.sh"}]}]}}' >"$codex_config"
jq -n '{permissions:{allow:["Read"]},hooks:{UserPromptSubmit:[{hooks:[{type:"command",command:"echo claude-start"},{type:"command",command:"bash /old/repo\\;meta/scripts/knowledge-deposition/claude-task-start.sh"}]}],Stop:[{hooks:[{type:"command",command:"echo claude-stop"},{type:"command",command:"bash /old/repo/scripts/knowledge-deposition/claude-stop.sh"},{type:"command",command:"true && bash /prior/scripts/knowledge-deposition/claude-stop.sh"}]}]}}' >"$claude_config"
codex_original="$(cat "$codex_config")"
claude_original="$(cat "$claude_config")"
install_out="$(CODEX_HOOKS_JSON="$codex_config" CLAUDE_SETTINGS_JSON="$claude_config" run_installer --repo-root "$installer_repo")" || fail "installer failed"
for spec in \
  "$codex_config|codex-task-start.sh|codex-stop.sh|echo codex-start|echo codex-stop" \
  "$claude_config|claude-task-start.sh|claude-stop.sh|echo claude-start|echo claude-stop"; do
  IFS='|' read -r config start_name stop_name unrelated_start unrelated_stop <<<"$spec"
  jq -e --arg root "$installer_repo" --arg start "$start_name" --arg stop "$stop_name" \
    --arg unrelated_start "$unrelated_start" --arg unrelated_stop "$unrelated_stop" '
      (.hooks.UserPromptSubmit | map(.hooks[]?.command) | map(select(. == ("bash " + $root + "/scripts/knowledge-deposition/" + $start))) | length) == 1 and
      (.hooks.Stop | map(.hooks[]?.command) | map(select(. == ("bash " + $root + "/scripts/knowledge-deposition/" + $stop))) | length) == 1 and
      all(.hooks.UserPromptSubmit[]?.hooks[]?; (.command | contains("/old/repo") | not)) and
      all(.hooks.Stop[]?.hooks[]?; (.command | contains("/old/repo") | not)) and
      any(.hooks.UserPromptSubmit[]?.hooks[]?; .command == $unrelated_start) and
      any(.hooks.Stop[]?.hooks[]?; .command == $unrelated_stop)
    ' "$config" >/dev/null || fail "installer did not preserve and install exactly one lifecycle pair: $config"
done
jq -e '
  any(.hooks.UserPromptSubmit[]?.hooks[]?; .command == "echo /prior/scripts/knowledge-deposition/codex-task-start.sh") and
  any(.hooks.UserPromptSubmit[]?.hooks[]?; .command == "bash /prior/scripts/knowledge-deposition/codex-task-start.sh --extra") and
  any(.hooks.Stop[]?.hooks[]?; .command == "bash /prior;echo/scripts/knowledge-deposition/codex-stop.sh")
' "$codex_config" >/dev/null || fail "Codex adapter-looking unrelated commands were removed"
jq -e '
  any(.hooks.Stop[]?.hooks[]?; .command == "true && bash /prior/scripts/knowledge-deposition/claude-stop.sh")
' "$claude_config" >/dev/null || fail "Claude adapter-looking unrelated command was removed"
jq -e '.theme == "dark"' "$codex_config" >/dev/null || fail "Codex unrelated root property lost"
jq -e '.permissions.allow == ["Read"]' "$claude_config" >/dev/null || fail "Claude unrelated root property lost"
jq -e '.hooks.UserPromptSubmit | any(.[]; .matcher == "keep-metadata" and (has("hooks") | not))' "$codex_config" >/dev/null || fail "entry without hooks was normalized or dropped"
first_codex="$(sha256sum "$codex_config")"; first_claude="$(sha256sum "$claude_config")"
backup_count="$(find "$installer_configs" -type f -name '*.knowledge-deposition.*.bak' | wc -l)"
CODEX_HOOKS_JSON="$codex_config" CLAUDE_SETTINGS_JSON="$claude_config" run_installer --repo-root "$installer_repo" >/dev/null || fail "idempotent installer run failed"
[[ "$(sha256sum "$codex_config")" == "$first_codex" && "$(sha256sum "$claude_config")" == "$first_claude" ]] || fail "second install changed canonical output"
[[ "$(find "$installer_configs" -type f -name '*.knowledge-deposition.*.bak' | wc -l)" == "$backup_count" ]] || fail "idempotent run created unnecessary backups"
[[ "$install_out" != *"$codex_original"* && "$install_out" != *"$claude_original"* ]] || fail "installer printed full settings"
[[ "$(stat -c %a "$codex_config")" == 600 && "$(stat -c %a "$claude_config")" == 600 ]] || \
  fail "published configs are not private"
pass "installer preserves unrelated JSON, installs one pair per client, and is byte-stable idempotent"

default_home="$FIXTURES/default-home"
mkdir -p "$default_home/.codex" "$default_home/.claude"
jq -n '{hooks:{},client:"codex-default"}' >"$default_home/.codex/hooks.json"
jq -n '{hooks:{},client:"claude-default"}' >"$default_home/.claude/settings.json"
HOME="$default_home" run_installer --repo-root "$installer_repo" >/dev/null || fail "installer did not use HOME-relative defaults"
jq -e '.client == "codex-default" and (.hooks.UserPromptSubmit | length) == 1 and (.hooks.Stop | length) == 1' \
  "$default_home/.codex/hooks.json" >/dev/null || fail "Codex default fixture was not installed"
jq -e '.client == "claude-default" and (.hooks.UserPromptSubmit | length) == 1 and (.hooks.Stop | length) == 1' \
  "$default_home/.claude/settings.json" >/dev/null || fail "Claude default fixture was not installed"
if HOME="$default_home" run_installer "$installer_repo" >/dev/null 2>&1; then fail "positional repository root accepted"; fi
pass "installer defaults are HOME-relative and the CLI accepts only --repo-root"

malformed_codex="$installer_configs/codex/malformed.json"
malformed_claude="$installer_configs/claude/malformed.json"
printf '{bad\n' >"$malformed_codex"; cp "$claude_config" "$malformed_claude"
malformed_codex_before="$(cat "$malformed_codex")"; malformed_claude_before="$(cat "$malformed_claude")"
if CODEX_HOOKS_JSON="$malformed_codex" CLAUDE_SETTINGS_JSON="$malformed_claude" run_installer --repo-root "$installer_repo" >/dev/null 2>&1; then fail "malformed input accepted"; fi
[[ "$(cat "$malformed_codex")" == "$malformed_codex_before" && "$(cat "$malformed_claude")" == "$malformed_claude_before" ]] || fail "malformed validation mutated inputs"
cp "$codex_config" "$malformed_codex"; printf '[]\n' >"$malformed_claude"
malformed_codex_before="$(cat "$malformed_codex")"; malformed_claude_before="$(cat "$malformed_claude")"
if CODEX_HOOKS_JSON="$malformed_codex" CLAUDE_SETTINGS_JSON="$malformed_claude" run_installer --repo-root "$installer_repo" >/dev/null 2>&1; then fail "non-object input accepted"; fi
[[ "$(cat "$malformed_codex")" == "$malformed_codex_before" && "$(cat "$malformed_claude")" == "$malformed_claude_before" ]] || fail "shape validation mutated inputs"
cp "$codex_config" "$malformed_codex"; jq '.hooks.Stop[0].hooks = ["bad"]' "$claude_config" >"$malformed_claude"
if CODEX_HOOKS_JSON="$malformed_codex" CLAUDE_SETTINGS_JSON="$malformed_claude" run_installer --repo-root "$installer_repo" >/dev/null 2>&1; then fail "malformed hook entry accepted"; fi
same_config="$installer_configs/same.json"; cp "$codex_config" "$same_config"
if CODEX_HOOKS_JSON="$same_config" CLAUDE_SETTINGS_JSON="$same_config" run_installer --repo-root "$installer_repo" >/dev/null 2>&1; then fail "identical config destinations accepted"; fi
pass "installer validates both JSON inputs before either mutation"

missing_root="$FIXTURES/missing-parent/codex/hooks.json"
missing_claude="$FIXTURES/missing-parent/claude/settings.json"
if CODEX_HOOKS_JSON="$missing_root" CLAUDE_SETTINGS_JSON="$missing_claude" run_installer --repo-root "$installer_repo" >/dev/null 2>&1; then fail "missing config parent accepted"; fi
[[ ! -e "$missing_root" && ! -e "$missing_claude" ]] || fail "missing config paths were created"
link_target="$installer_configs/link-target.json"; cp "$claude_config" "$link_target"
link_config="$installer_configs/claude/settings-link.json"; ln -s "$link_target" "$link_config"
if CODEX_HOOKS_JSON="$codex_config" CLAUDE_SETTINGS_JSON="$link_config" run_installer --repo-root "$installer_repo" >/dev/null 2>&1; then fail "symlink config accepted"; fi
pass "installer rejects missing parents and symlink destinations"

rollback_dir="$FIXTURES/rollback"
mkdir -p "$rollback_dir/codex" "$rollback_dir/claude" "$rollback_dir/bin"
rollback_codex="$rollback_dir/codex/hooks.json"; rollback_claude="$rollback_dir/claude/settings.json"
jq -n '{hooks:{},keep:"codex-original"}' >"$rollback_codex"; jq -n '{hooks:{},keep:"claude-original"}' >"$rollback_claude"
rollback_codex_before="$(cat "$rollback_codex")"; rollback_claude_before="$(cat "$rollback_claude")"
real_mv="$(command -v mv)"
printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail' 'count_file="${KD_MV_COUNT}"' 'count=0' '[[ ! -f "$count_file" ]] || count="$(cat "$count_file")"' 'count=$((count + 1))' 'printf "%s\n" "$count" >"$count_file"' 'if [[ "$count" -eq 2 ]]; then exit 73; fi' 'exec "$KD_REAL_MV" "$@"' >"$rollback_dir/bin/mv"
chmod +x "$rollback_dir/bin/mv"
if PATH="$rollback_dir/bin:$PATH" KD_MV_COUNT="$rollback_dir/mv.count" KD_REAL_MV="$real_mv" CODEX_HOOKS_JSON="$rollback_codex" CLAUDE_SETTINGS_JSON="$rollback_claude" run_installer --repo-root "$installer_repo" >"$rollback_dir/out" 2>"$rollback_dir/err"; then fail "injected second rename failure succeeded"; fi
[[ "$(cat "$rollback_codex")" == "$rollback_codex_before" && "$(cat "$rollback_claude")" == "$rollback_claude_before" ]] || fail "transaction rollback did not preserve both originals"
while IFS= read -r backup; do
  [[ "$(stat -c %a "$backup")" == 600 ]] || fail "backup permissions are not private: $backup"
done < <(find "$rollback_dir" -type f -name '*.bak')
pass "second rename failure restores the first file and preserves both originals"

backup_failure_dir="$FIXTURES/backup-failure"
mkdir -p "$backup_failure_dir/codex" "$backup_failure_dir/claude" "$backup_failure_dir/bin"
backup_failure_codex="$backup_failure_dir/codex/hooks.json"
backup_failure_claude="$backup_failure_dir/claude/settings.json"
jq -n '{hooks:{},keep:"codex-edit"}' >"$backup_failure_codex"
jq -n '{hooks:{},keep:"claude-edit"}' >"$backup_failure_claude"
backup_failure_codex_before="$(cat "$backup_failure_codex")"
backup_failure_claude_before="$(cat "$backup_failure_claude")"
real_cp="$(command -v cp)"
printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail' 'count_file="${KD_CP_COUNT}"' 'count=0' \
  '[[ ! -f "$count_file" ]] || count="$(cat "$count_file")"' 'count=$((count + 1))' \
  'printf "%s\n" "$count" >"$count_file"' 'if [[ "$count" -eq 2 ]]; then exit 75; fi' \
  'exec "$KD_REAL_CP" "$@"' >"$backup_failure_dir/bin/cp"
chmod +x "$backup_failure_dir/bin/cp"
if PATH="$backup_failure_dir/bin:$PATH" KD_CP_COUNT="$backup_failure_dir/cp.count" KD_REAL_CP="$real_cp" \
  CODEX_HOOKS_JSON="$backup_failure_codex" CLAUDE_SETTINGS_JSON="$backup_failure_claude" \
  run_installer --repo-root "$installer_repo" >"$backup_failure_dir/out" 2>"$backup_failure_dir/err"; then
  fail "injected second backup failure succeeded"
fi
[[ "$(cat "$backup_failure_codex")" == "$backup_failure_codex_before" && \
   "$(cat "$backup_failure_claude")" == "$backup_failure_claude_before" ]] || fail "backup failure mutated configs"
if find "$backup_failure_dir" -type f \( -name '*.bak' -o -name '.hooks.json.knowledge-deposition.*' -o \
  -name '.settings.json.knowledge-deposition.*' \) -print -quit | grep -q .; then
  fail "backup failure left an orphan backup or temp"
fi
pass "second backup failure cleans backups and temps before mutation"

cas_dir="$FIXTURES/cas-edit"
mkdir -p "$cas_dir/codex" "$cas_dir/claude" "$cas_dir/bin"
cas_codex="$cas_dir/codex/hooks.json"
cas_claude="$cas_dir/claude/settings.json"
jq -n '{hooks:{},keep:"codex-before"}' >"$cas_codex"
jq -n '{hooks:{},keep:"claude-before"}' >"$cas_claude"
real_sha256sum="$(command -v sha256sum)"
printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail' 'count_file="${KD_SHA_COUNT}"' 'count=0' \
  '[[ ! -f "$count_file" ]] || count="$(cat "$count_file")"' 'count=$((count + 1))' \
  'printf "%s\n" "$count" >"$count_file"' \
  'if [[ "$count" -eq 3 ]]; then printf "{\"hooks\":{},\"keep\":\"external-edit\"}\n" >"$KD_EDIT_CONFIG"; fi' \
  'exec "$KD_REAL_SHA256SUM" "$@"' >"$cas_dir/bin/sha256sum"
chmod +x "$cas_dir/bin/sha256sum"
if PATH="$cas_dir/bin:$PATH" KD_SHA_COUNT="$cas_dir/sha.count" KD_EDIT_CONFIG="$cas_codex" \
  KD_REAL_SHA256SUM="$real_sha256sum" CODEX_HOOKS_JSON="$cas_codex" CLAUDE_SETTINGS_JSON="$cas_claude" \
  run_installer --repo-root "$installer_repo" >"$cas_dir/out" 2>"$cas_dir/err"; then
  fail "concurrent config edit was not detected"
fi
jq -e '.keep == "external-edit" and .hooks == {}' "$cas_codex" >/dev/null || fail "concurrent edit was overwritten"
jq -e '.keep == "claude-before" and .hooks == {}' "$cas_claude" >/dev/null || fail "untouched config was mutated"
if find "$cas_dir" -type f \( -name '*.bak' -o -name '.hooks.json.knowledge-deposition.*' -o \
  -name '.settings.json.knowledge-deposition.*' \) -print -quit | grep -q .; then
  fail "concurrent edit abort left a backup or temp"
fi
grep -Fq 'configuration changed during installation' "$cas_dir/err" || fail "concurrent edit error was not actionable"
pass "digest check preserves a noncooperating edit detected before publish"

lock_target="$FIXTURES/installer-lock-target"
printf 'unchanged\n' >"$lock_target"
rm -f "${cas_codex}.knowledge-deposition.lock"
ln -s "$lock_target" "${cas_codex}.knowledge-deposition.lock"
if CODEX_HOOKS_JSON="$cas_codex" CLAUDE_SETTINGS_JSON="$cas_claude" \
  run_installer --repo-root "$installer_repo" >/dev/null 2>&1; then
  fail "symlink installer lock was accepted"
fi
[[ "$(cat "$lock_target")" == unchanged ]] || fail "symlink installer lock target was modified"
pass "installer advisory locks are private and symlink-safe"

cooperate_dir="$FIXTURES/cooperating-installers"
mkdir -p "$cooperate_dir/codex" "$cooperate_dir/claude"
cooperate_codex="$cooperate_dir/codex/hooks.json"
cooperate_claude="$cooperate_dir/claude/settings.json"
jq -n '{hooks:{},keep:"codex"}' >"$cooperate_codex"
jq -n '{hooks:{},keep:"claude"}' >"$cooperate_claude"
pids=()
for index in 1 2; do
  CODEX_HOOKS_JSON="$cooperate_codex" CLAUDE_SETTINGS_JSON="$cooperate_claude" \
    run_installer --repo-root "$installer_repo" >"$cooperate_dir/$index.out" 2>"$cooperate_dir/$index.err" &
  pids+=("$!")
done
for pid in "${pids[@]}"; do
  for _ in $(seq 1 500); do kill -0 "$pid" 2>/dev/null || break; sleep 0.02; done
  kill -0 "$pid" 2>/dev/null && { kill "$pid" 2>/dev/null || true; fail "cooperating installer exceeded bounded wait"; }
  wait "$pid" || fail "cooperating installer failed"
done
jq -e '(.hooks.UserPromptSubmit | length) == 1 and (.hooks.Stop | length) == 1 and .keep == "codex"' \
  "$cooperate_codex" >/dev/null || fail "concurrent Codex install was inconsistent"
jq -e '(.hooks.UserPromptSubmit | length) == 1 and (.hooks.Stop | length) == 1 and .keep == "claude"' \
  "$cooperate_claude" >/dev/null || fail "concurrent Claude install was inconsistent"
[[ "$(stat -c %a "${cooperate_codex}.knowledge-deposition.lock")" == 600 && \
   "$(stat -c %a "${cooperate_claude}.knowledge-deposition.lock")" == 600 ]] || fail "installer locks are not private"
pass "deterministically ordered advisory locks serialize cooperating installers"

chmod_failure_dir="$FIXTURES/chmod-publish"
mkdir -p "$chmod_failure_dir/codex" "$chmod_failure_dir/claude" "$chmod_failure_dir/bin"
chmod_failure_codex="$chmod_failure_dir/codex/hooks.json"
chmod_failure_claude="$chmod_failure_dir/claude/settings.json"
jq -n '{hooks:{}}' >"$chmod_failure_codex"
jq -n '{hooks:{}}' >"$chmod_failure_claude"
real_chmod="$(command -v chmod)"
printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail' 'count_file="${KD_CHMOD_COUNT}"' 'count=0' \
  '[[ ! -f "$count_file" ]] || count="$(cat "$count_file")"' 'count=$((count + 1))' \
  'printf "%s\n" "$count" >"$count_file"' 'if [[ "$count" -eq 5 ]]; then exit 76; fi' \
  'exec "$KD_REAL_CHMOD" "$@"' >"$chmod_failure_dir/bin/chmod"
chmod +x "$chmod_failure_dir/bin/chmod"
PATH="$chmod_failure_dir/bin:$PATH" KD_CHMOD_COUNT="$chmod_failure_dir/chmod.count" KD_REAL_CHMOD="$real_chmod" \
  CODEX_HOOKS_JSON="$chmod_failure_codex" CLAUDE_SETTINGS_JSON="$chmod_failure_claude" \
  run_installer --repo-root "$installer_repo" >/dev/null || fail "installer depended on post-publish chmod"
[[ "$(stat -c %a "$chmod_failure_codex")" == 600 && "$(stat -c %a "$chmod_failure_claude")" == 600 ]] || \
  fail "published configs lost private mode without post-publish chmod"
pass "published configs are mode 600 before replacement with no post-commit chmod failure point"

printf '1..%d\n' "$count"
