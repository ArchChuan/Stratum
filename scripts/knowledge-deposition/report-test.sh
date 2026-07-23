#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPORT_SH="$SCRIPT_DIR/report.sh"
FIXTURE_ROOT="$(mktemp -d)"
trap 'rm -rf "$FIXTURE_ROOT"' EXIT

pass_count=0

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

pass() {
  pass_count=$((pass_count + 1))
  printf 'ok %d - %s\n' "$pass_count" "$1"
}

new_repo() {
  local name="$1"
  local repo="$FIXTURE_ROOT/$name"
  mkdir -p "$repo/docs/agent" "$repo/tmp/knowledge-deposition/current"
  printf 'module github.com/ArchChuan/Stratum\n\ngo 1.25.12\n' >"$repo/go.mod"
  printf '# Knowledge deposition policy\n' >"$repo/docs/agent/knowledge-deposition.md"
  git -C "$repo" init -q
  git -C "$repo" config user.email test@example.com
  git -C "$repo" config user.name Test
  git -C "$repo" add go.mod docs/agent/knowledge-deposition.md
  git -C "$repo" commit -qm initial
  printf '%s\n' "$repo"
}

write_marker() {
  local repo="$1" client="$2" session="$3" task="$4"
  jq -cn --arg client "$client" --arg session "$session" --arg task "$task" --arg root "$repo" \
    '{schema_version:1,client:$client,session_id:$session,task_id:$task,repository:{root:$root},created_at:"2026-07-23T00:00:00Z"}' \
    >"$repo/tmp/knowledge-deposition/current/$client-$session.json"
}

candidate_json() {
  jq -n '{
    id: "candidate-1",
    claim: "Atomic reports preserve task-end evidence.",
    destination: "project_git",
    evidence: [{path: "scripts/knowledge-deposition/report.sh", anchor: "writer"}],
    scope: "Stratum task-end reporting",
    exclusions: ["Does not write to external knowledge stores."],
    duplicate_result: "new",
    target: "docs/agent/knowledge-deposition.md",
    confidence: "high"
  }'
}

valid_candidates() {
  jq -n --argjson candidate "$(candidate_json)" '{
    decision: "candidates",
    task_summary: "Implemented report persistence.",
    none_reason: null,
    candidates: [$candidate]
  }'
}

valid_none() {
  jq -n '{
    decision: "none",
    task_summary: "Routine maintenance only.",
    none_reason: "No reusable knowledge was produced.",
    candidates: []
  }'
}

run_report() {
  local repo="$1" client="$2" session="$3" task="$4" input="$5"
  printf '%s' "$input" | "$REPORT_SH" \
    --client "$client" --session "$session" --task "$task" --repo-root "$repo"
}

expect_reject() {
  local name="$1" repo="$2" input="$3"
  write_marker "$repo" codex session-a task-a
  if run_report "$repo" codex session-a task-a "$input" >"$FIXTURE_ROOT/out" 2>"$FIXTURE_ROOT/err"; then
    fail "$name: unexpectedly accepted"
  fi
  pass "$name"
}

repo="$(new_repo valid)"
write_marker "$repo" codex session-a task-a
output="$(run_report "$repo" codex session-a task-a "$(valid_candidates)")"
[[ "$output" = /*.md ]] || fail "valid candidates: expected absolute Markdown path"
json_path="${output%.md}.json"
jq -e '
  .schema_version == 1 and .client == "codex" and
  .session_id == "session-a" and .task_id == "task-a" and
  .repository.root == $root and (.repository.commit | length == 40) and
  (.created_at | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T")) and
  .decision == "candidates" and (.candidates | length == 1)
' --arg root "$repo" "$json_path" >/dev/null || fail "valid candidates: normalized JSON mismatch"
jq -e '[paths(objects) as $p | (getpath($p) | keys_unsorted[]) | ascii_downcase |
  select(. == "transcript" or . == "prompt" or . == "password" or . == "secret" or
    . == "token" or . == "api_key" or . == "raw_response")] | length == 0' \
  "$json_path" >/dev/null || fail "valid candidates: forbidden key persisted"
grep -Fq '# Knowledge deposition report' "$output" || fail "valid candidates: missing Markdown heading"
grep -Fq 'Atomic reports preserve task-end evidence.' "$output" || fail "valid candidates: missing claim"
grep -Fq 'scripts/knowledge-deposition/report.sh\#writer' "$output" || fail "valid candidates: missing evidence anchor"
if grep -Eiq 'transcript|prompt|password|secret|token|api_key|raw_response' "$output"; then
  fail "valid candidates: forbidden content persisted to Markdown"
fi
latest_valid="$repo/tmp/knowledge-deposition/latest.md"
relative_base="$(basename "$(dirname "$output")")/$(basename "${output%.md}")"
grep -Fq "[$relative_base.json]($relative_base.json)" "$latest_valid" || \
  fail "valid candidates: latest pointer missing JSON artifact"
grep -Fq "[$relative_base.md]($relative_base.md)" "$latest_valid" || \
  fail "valid candidates: latest pointer missing Markdown artifact"
pass "valid candidates are normalized and rendered"

repo_none="$(new_repo none)"
write_marker "$repo_none" claude session-b task-b
none_output="$(run_report "$repo_none" claude session-b task-b "$(valid_none)")"
jq -e '.decision == "none" and .candidates == [] and .none_reason == "No reusable knowledge was produced."' \
  "${none_output%.md}.json" >/dev/null || fail "valid none: normalized JSON mismatch"
grep -Fq 'No reusable knowledge was produced.' "$none_output" || fail "valid none: missing reason"
pass "valid none decision is persisted"

repo_invalid="$(new_repo invalid)"
expect_reject "invalid destination is rejected" "$repo_invalid" \
  "$(valid_candidates | jq '.candidates[0].destination = "email"')"
expect_reject "none with candidates is rejected" "$repo_invalid" \
  "$(valid_candidates | jq '.decision = "none" | .none_reason = "nothing"')"
expect_reject "candidates with empty array is rejected" "$repo_invalid" \
  "$(valid_candidates | jq '.candidates = []')"
expect_reject "absolute evidence path is rejected" "$repo_invalid" \
  "$(valid_candidates | jq '.candidates[0].evidence[0].path = "/etc/passwd"')"
expect_reject "parent traversal evidence path is rejected" "$repo_invalid" \
  "$(valid_candidates | jq '.candidates[0].evidence[0].path = "docs/../secret"')"

for key in transcript prompt password secret token api_key raw_response TrAnScRiPt; do
  expect_reject "recursive forbidden key $key is rejected" "$repo_invalid" \
    "$(valid_candidates | jq --arg key "$key" '.candidates[0].metadata.nested[$key] = "redacted-value"')"
done

obsidian_base="$(valid_candidates | jq '
  .candidates[0].destination = "obsidian" |
  .candidates[0].knowledge_type = "principle" |
  .candidates[0].vault_queries = ["atomic knowledge reports"] |
  .candidates[0].related_notes = ["99-系统/知识输入与证据检索协议.md"] |
  .candidates[0].verification_status = "verified" |
  .candidates[0].governance_action = "create"
')"
for field in knowledge_type vault_queries related_notes verification_status governance_action; do
  expect_reject "Obsidian candidate missing $field is rejected" "$repo_invalid" \
    "$(printf '%s' "$obsidian_base" | jq --arg field "$field" 'del(.candidates[0][$field])')"
done

duplicate_claim_group="$(valid_candidates | jq '
  .candidates += [(.candidates[0] | .id = "candidate-2" | .destination = "skill" | .target = "skill-name")] |
  .candidates[0].claim_group = "atomic-reporting" |
  .candidates[1].claim_group = "atomic-reporting"
')"
expect_reject "multi-destination claim group without consumption purposes is rejected" "$repo_invalid" "$duplicate_claim_group"
accepted_distinct="$(printf '%s' "$duplicate_claim_group" | jq '
  .candidates[0].consumption_purpose = "repository contract" |
  .candidates[1].consumption_purpose = "reusable execution procedure"
')"
write_marker "$repo_invalid" codex session-a task-a
run_report "$repo_invalid" codex session-a task-a "$accepted_distinct" >/dev/null || \
  fail "distinct consumption purposes should be accepted"
pass "multi-destination claim group accepts distinct consumption purposes"

expect_reject "unknown top-level fields are rejected" "$repo_invalid" \
  "$(valid_none | jq '.unexpected = true')"
expect_reject "unknown candidate fields are rejected" "$repo_invalid" \
  "$(valid_candidates | jq '.candidates[0].unexpected = true')"

write_marker "$repo_invalid" codex session-a task-a
if printf '%s\n%s\n' "$(valid_none)" "$(valid_none)" | "$REPORT_SH" --client codex \
  --session session-a --task task-a --repo-root "$repo_invalid" >/dev/null 2>&1; then
  fail "two concatenated JSON objects were accepted"
fi
pass "two concatenated JSON objects are rejected"

write_marker "$repo_invalid" codex session-a task-a
if { printf '%s\n' "$(valid_none)"; printf '42\n'; } | "$REPORT_SH" --client codex \
  --session session-a --task task-a --repo-root "$repo_invalid" >/dev/null 2>&1; then
  fail "JSON object followed by scalar was accepted"
fi
pass "JSON object followed by scalar is rejected"

raw_marker='forbidden-payload-must-never-persist-92741'
write_marker "$repo_invalid" codex session-a task-a
forbidden_payload="$(valid_candidates | jq --arg value "$raw_marker" '.candidates[0].secret = $value')"
if run_report "$repo_invalid" codex session-a task-a "$forbidden_payload" >/dev/null 2>&1; then
  fail "forbidden payload was accepted"
fi
if find "$repo_invalid/tmp/knowledge-deposition" -name '.input*' -print -quit | grep -q .; then
  fail "rejected payload left an input staging file"
fi
if grep -RFl -- "$raw_marker" "$repo_invalid/tmp/knowledge-deposition" >/dev/null; then
  fail "rejected payload persisted raw secret content"
fi
pass "rejected payload leaves no raw input staging or secret content"

for sensitive_value in \
  'Bearer '"abcdefghijklmnopqrstuvwxyz" \
  'sk-'"abcdefghijklmnopqrstuvwxyz123456" \
  'eyJhbGciOiJIUzI1NiJ9''.''eyJzdWIiOiIxMjM0NTY3ODkwIn0''.''c2lnbmF0dXJlMTIzNDU2' \
  'token='"abcdefghijklmnopqrstuvwxyz" \
  'API_KEY: '"abcdefghijklmnopqrstuvwxyz"; do
  expect_reject "high-signal sensitive value is rejected" "$repo_invalid" \
    "$(valid_none | jq --arg value "$sensitive_value" '.task_summary = $value')"
done
expect_reject "newline structural injection is rejected" "$repo_invalid" \
  "$(valid_none | jq '.task_summary = "summary\n# injected heading"')"
expect_reject "control character injection is rejected" "$repo_invalid" \
  "$(valid_none | jq '.task_summary = "summary\twith tab"')"

markdown_safe="$(valid_candidates | jq '
  .task_summary = "Summary # heading [link](target) `code` | cell" |
  .candidates[0].claim = "Claim # heading [link](target) `code` | cell" |
  .candidates[0].scope = "Scope * emphasis _ underline" |
  .candidates[0].target = "docs/`target`` # heading [link](target) | cell.md" |
  .candidates[0].evidence[0].path = "scripts/`evidence``#heading|cell.md" |
  .candidates[0].evidence[0].anchor = "anchor```#heading[link](target)|cell" |
  .candidates[0].claim_group = "group```#heading[link](target)|cell"
')"
repo_markdown_safe="$(new_repo 'markdown-root-`tick``')"
write_marker "$repo_markdown_safe" codex markdown-safe markdown-safe
markdown_safe_path="$(run_report "$repo_markdown_safe" codex markdown-safe markdown-safe "$markdown_safe")"
grep -Fq 'Summary \# heading \[link\]\(target\) \`code\` \| cell' "$markdown_safe_path" || \
  fail "Markdown task summary was not escaped"
grep -Fq 'Claim \# heading \[link\]\(target\) \`code\` \| cell' "$markdown_safe_path" || \
  fail "Markdown claim was not escaped"
grep -Fq 'Target: docs/\`target\`\` \# heading \[link\]\(target\) \| cell.md' "$markdown_safe_path" || \
  fail "Markdown target metacharacters were not escaped safely"
grep -Fq 'Evidence: scripts/\`evidence\`\`\#heading\|cell.md\#anchor\`\`\`\#heading\[link\]\(target\)\|cell' \
  "$markdown_safe_path" || fail "Markdown evidence metacharacters were not escaped safely"
grep -Fq "Repository: ${repo_markdown_safe//\`/\\\`}" "$markdown_safe_path" || \
  fail "Markdown repository root backticks were not escaped safely"
grep -Fq 'Claim group: group\`\`\`\#heading\[link\]\(target\)\|cell' "$markdown_safe_path" || \
  fail "Markdown claim group metacharacters were not escaped safely"
if grep -E '^- Repository: |^- Target: |^  - Evidence: |^- Claim group: ' "$markdown_safe_path" | \
  grep -Eq '(^|[^\\])`'; then
  fail "Markdown renderer emitted an unsafe code span delimiter"
fi
pass "Markdown metacharacters are escaped"

outside_tmp="$FIXTURE_ROOT/outside-tmp"
repo_tmp_link="$(new_repo tmp-link)"
rm -rf "$repo_tmp_link/tmp"
mkdir -p "$outside_tmp/knowledge-deposition/current"
ln -s "$outside_tmp" "$repo_tmp_link/tmp"
printf '{"task_id":"task-a"}\n' >"$outside_tmp/knowledge-deposition/current/codex-session-a.json"
if run_report "$repo_tmp_link" codex session-a task-a "$(valid_none)" >/dev/null 2>&1; then
  fail "tmp symlink escape was accepted"
fi
[[ ! -e "$outside_tmp/knowledge-deposition/.lock" ]] || fail "tmp symlink escape wrote outside repository"
pass "tmp symlink escape is rejected"

outside_kd="$FIXTURE_ROOT/outside-kd"
repo_kd_link="$(new_repo kd-link)"
rm -rf "$repo_kd_link/tmp/knowledge-deposition"
mkdir -p "$outside_kd/current"
ln -s "$outside_kd" "$repo_kd_link/tmp/knowledge-deposition"
printf '{"task_id":"task-a"}\n' >"$outside_kd/current/codex-session-a.json"
if run_report "$repo_kd_link" codex session-a task-a "$(valid_none)" >/dev/null 2>&1; then
  fail "knowledge-deposition symlink escape was accepted"
fi
[[ ! -e "$outside_kd/.lock" ]] || fail "knowledge-deposition symlink escape wrote outside repository"
pass "knowledge-deposition symlink escape is rejected"

repo_lock_link="$(new_repo lock-link)"
write_marker "$repo_lock_link" codex session-a task-a
lock_target="$FIXTURE_ROOT/lock-target"
printf 'do-not-truncate\n' >"$lock_target"
ln -s "$lock_target" "$repo_lock_link/tmp/knowledge-deposition/.lock"
if run_report "$repo_lock_link" codex session-a task-a "$(valid_none)" >/dev/null 2>&1; then
  fail "lock symlink was accepted"
fi
[[ "$(cat "$lock_target")" == 'do-not-truncate' ]] || fail "lock symlink target was modified"
pass "lock symlink target is never truncated"

repo_lock_file_link="$(new_repo lock-file-link)"
write_marker "$repo_lock_file_link" codex session-a task-a
mkdir -p "$repo_lock_file_link/tmp/knowledge-deposition/.lock"
lock_file_target="$FIXTURE_ROOT/lock-file-target"
printf 'do-not-modify\n' >"$lock_file_target"
ln -s "$lock_file_target" "$repo_lock_file_link/tmp/knowledge-deposition/.lock/report.lock"
if run_report "$repo_lock_file_link" codex session-a task-a "$(valid_none)" >/dev/null 2>&1; then
  fail "preexisting lock file symlink was accepted"
fi
[[ "$(cat "$lock_file_target")" == 'do-not-modify' ]] || fail "preexisting lock symlink target was modified"
pass "preexisting lock file symlink target is never modified"

repo_dangling_lock="$(new_repo dangling-lock)"
write_marker "$repo_dangling_lock" codex session-a task-a
mkdir -p "$repo_dangling_lock/tmp/knowledge-deposition/.lock"
dangling_target="$FIXTURE_ROOT/dangling-lock-target"
ln -s "$dangling_target" "$repo_dangling_lock/tmp/knowledge-deposition/.lock/report.lock"
if run_report "$repo_dangling_lock" codex session-a task-a "$(valid_none)" >/dev/null 2>&1; then
  fail "dangling lock file symlink was accepted"
fi
[[ ! -e "$dangling_target" ]] || fail "dangling lock symlink target was created"
pass "dangling lock file symlink target is never created"

repo_pair="$(new_repo pair)"
write_marker "$repo_pair" codex session-a task-a
if KNOWLEDGE_REPORT_TEST_FAIL_SECOND_PUBLISH=1 run_report "$repo_pair" codex session-a task-a \
  "$(valid_none)" >/dev/null 2>&1; then
  fail "injected second publish failure unexpectedly succeeded"
fi
if find "$repo_pair/tmp/knowledge-deposition" -mindepth 2 -type f \
  \( -name 'codex-session-a-task-a.json' -o -name 'codex-session-a-task-a.md' \) | grep -q .; then
  fail "second publish failure left a partial authoritative pair"
fi
pass "second publish failure rolls back the report pair"

write_marker "$repo_pair" codex session-b task-b
pair_path="$(run_report "$repo_pair" codex session-b task-b "$(valid_none)")"
pair_json="${pair_path%.md}.json"
json_hash="$(sha256sum "$pair_json" | awk '{print $1}')"
md_hash="$(sha256sum "$pair_path" | awk '{print $1}')"
if run_report "$repo_pair" codex session-b task-b "$(valid_none | jq '.task_summary = "retry overwrite"')" \
  >/dev/null 2>&1; then
  fail "existing report pair was overwritten on retry"
fi
[[ "$(sha256sum "$pair_json" | awk '{print $1}')" == "$json_hash" ]] || fail "retry changed existing JSON"
[[ "$(sha256sum "$pair_path" | awk '{print $1}')" == "$md_hash" ]] || fail "retry changed existing Markdown"
pass "existing report pair is collision protected"

repo_marker_race="$(new_repo marker-race)"
write_marker "$repo_marker_race" codex race-session old-task
mkdir -p "$repo_marker_race/tmp/knowledge-deposition/.lock"
marker_lock="$repo_marker_race/tmp/knowledge-deposition/.lock/report.lock"
: >"$marker_lock"
exec {marker_lock_fd}>>"$marker_lock"
flock "$marker_lock_fd"
run_report "$repo_marker_race" codex race-session old-task "$(valid_none)" \
  >"$FIXTURE_ROOT/marker-race.out" 2>"$FIXTURE_ROOT/marker-race.err" &
marker_pid=$!
for _ in $(seq 1 50); do
  kill -0 "$marker_pid" 2>/dev/null || fail "stale reporter exited before publication lock release"
  sleep 0.02
done
marker_tmp="$repo_marker_race/tmp/knowledge-deposition/current/.marker.XXXXXX"
marker_tmp="$(mktemp "$marker_tmp")"
printf '{"task_id":"new-task"}\n' >"$marker_tmp"
mv "$marker_tmp" "$repo_marker_race/tmp/knowledge-deposition/current/codex-race-session.json"
flock -u "$marker_lock_fd"
exec {marker_lock_fd}>&-
for _ in $(seq 1 100); do
  kill -0 "$marker_pid" 2>/dev/null || break
  sleep 0.02
done
if kill -0 "$marker_pid" 2>/dev/null; then
  kill "$marker_pid" 2>/dev/null || true
  wait "$marker_pid" 2>/dev/null || true
  fail "stale reporter did not exit within bounded wait"
fi
marker_status=0
wait "$marker_pid" || marker_status=$?
if [[ "$marker_status" -eq 0 ]]; then
  fail "stale task published after current marker advanced"
fi
if find "$repo_marker_race/tmp/knowledge-deposition" -mindepth 2 -type f -name '*old-task*' | grep -q .; then
  fail "stale task left report artifacts"
fi
pass "marker advancement under the publication lock rejects stale task"

repo_legacy_env="$(new_repo legacy-env)"
write_marker "$repo_legacy_env" codex legacy-session legacy-task
legacy_ready="$FIXTURE_ROOT/legacy-ready-must-not-exist"
legacy_continue="$FIXTURE_ROOT/legacy-continue"
: >"$legacy_continue"
KNOWLEDGE_REPORT_TEST_LOCK_READY="$legacy_ready" KNOWLEDGE_REPORT_TEST_LOCK_CONTINUE="$legacy_continue" \
  run_report "$repo_legacy_env" codex legacy-session legacy-task "$(valid_none)" >/dev/null || \
  fail "legacy environment variables disrupted report publication"
[[ ! -e "$legacy_ready" ]] || fail "legacy environment variable caused an arbitrary path write"
pass "legacy test-control environment variables cannot mutate outside paths"

write_marker "$repo_invalid" codex session-a different-task
if run_report "$repo_invalid" codex session-a task-a "$(valid_none)" >/dev/null 2>&1; then
  fail "task binding mismatch was accepted"
fi
pass "current marker task binding is exact"

write_marker "$repo_invalid" codex session-a task-a
if printf '%s' "$(valid_none)" | "$REPORT_SH" --client codex --session session-a --task task-a \
  --repo-root "$repo_invalid" --output anywhere >/dev/null 2>&1; then
  fail "extra CLI flag was accepted"
fi
pass "extra CLI flags are rejected"

non_stratum="$FIXTURE_ROOT/non-stratum"
mkdir -p "$non_stratum/docs/agent" "$non_stratum/tmp/knowledge-deposition/current"
printf 'module example.com/not-stratum\n' >"$non_stratum/go.mod"
printf '# policy\n' >"$non_stratum/docs/agent/knowledge-deposition.md"
git -C "$non_stratum" init -q
git -C "$non_stratum" config user.email test@example.com
git -C "$non_stratum" config user.name Test
git -C "$non_stratum" add .
git -C "$non_stratum" commit -qm initial
write_marker "$non_stratum" codex session-a task-a
if run_report "$non_stratum" codex session-a task-a "$(valid_none)" >/dev/null 2>&1; then
  fail "non-Stratum repository was accepted"
fi
pass "non-Stratum repository is rejected"

printf '{not-json\n' >"$repo_invalid/tmp/knowledge-deposition/current/codex-session-a.json"
if run_report "$repo_invalid" codex session-a task-a "$(valid_none)" >/dev/null 2>&1; then
  fail "invalid current marker was accepted"
fi
pass "invalid current marker is rejected"

repo_concurrent="$(new_repo concurrent)"
pids=()
for i in $(seq 1 20); do
  session="session-$i"
  task="task-$i"
  write_marker "$repo_concurrent" codex "$session" "$task"
  run_report "$repo_concurrent" codex "$session" "$task" "$(valid_none)" \
    >"$FIXTURE_ROOT/concurrent-$i.out" 2>"$FIXTURE_ROOT/concurrent-$i.err" &
  pids+=("$!")
done
for pid in "${pids[@]}"; do
  wait "$pid" || fail "concurrent writer failed"
done
mapfile -t reports < <(find "$repo_concurrent/tmp/knowledge-deposition" -mindepth 2 -name '*.json' \
  ! -path '*/current/*' -print)
[[ "${#reports[@]}" -eq 20 ]] || fail "expected 20 concurrent JSON reports, got ${#reports[@]}"
for report in "${reports[@]}"; do
  jq -e . "$report" >/dev/null || fail "invalid concurrent JSON: $report"
done
latest="$repo_concurrent/tmp/knowledge-deposition/latest.md"
[[ "$(grep -c '^- \[' "$latest")" -eq 40 ]] || fail "latest pointer missing paired artifact entries"
[[ "$(grep -c '\.json)$' "$latest")" -eq 20 ]] || fail "latest pointer missing JSON entries"
[[ "$(grep -c '\.md)$' "$latest")" -eq 20 ]] || fail "latest pointer missing Markdown entries"
if grep -Ev '^(# Latest knowledge deposition reports|- \[[^]]+\]\([^()]+\.(json|md)\))$' "$latest" | grep -q .; then
  fail "latest pointer contains partial or malformed lines"
fi
pass "20 concurrent writers produce complete reports and latest pointer"

printf '1..%d\n' "$pass_count"
