#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/common.sh"
CODEX_START="$SCRIPT_DIR/codex-task-start.sh"
CODEX_STOP="$SCRIPT_DIR/codex-stop.sh"
CLAUDE_START="$SCRIPT_DIR/claude-task-start.sh"
CLAUDE_STOP="$SCRIPT_DIR/claude-stop.sh"
REPORT="$SCRIPT_DIR/report.sh"
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

printf '1..%d\n' "$count"
