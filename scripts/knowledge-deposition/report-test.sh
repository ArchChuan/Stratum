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
  printf '{"task_id":"%s"}\n' "$task" >"$repo/tmp/knowledge-deposition/current/$client-$session.json"
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
grep -Fq 'scripts/knowledge-deposition/report.sh#writer' "$output" || fail "valid candidates: missing evidence anchor"
if grep -Eiq 'transcript|prompt|password|secret|token|api_key|raw_response' "$output"; then
  fail "valid candidates: forbidden content persisted to Markdown"
fi
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
[[ "$(grep -c '^- \[' "$latest")" -eq 20 ]] || fail "latest pointer missing entries"
if grep -Ev '^(# Latest knowledge deposition reports|- \[[^]]+\]\([^()]+\.md\))$' "$latest" | grep -q .; then
  fail "latest pointer contains partial or malformed lines"
fi
pass "20 concurrent writers produce complete reports and latest pointer"

printf '1..%d\n' "$pass_count"
