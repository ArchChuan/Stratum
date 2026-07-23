#!/usr/bin/env bash

knowledge_fail() {
  printf 'knowledge deposition: %s\n' "$1" >&2
  return 1
}

knowledge_safe_client() {
  case "${1:-}" in
    codex | claude) printf '%s\n' "$1" ;;
    *) knowledge_fail 'client must be codex or claude' ;;
  esac
}

knowledge_sanitize_identifier() {
  local value="${1:-}"
  [[ -n "$value" && ${#value} -le 128 ]] || return 1
  [[ "$value" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || return 1
  [[ "$value" != *..* ]] || return 1
  printf '%s\n' "$value"
}

knowledge_sanitize_session() {
  knowledge_sanitize_identifier "${1:-}" || knowledge_fail 'session must be a safe filename identifier of at most 128 characters'
}

knowledge_normalize_session() {
  local value
  value="$(printf '%s' "${1:-}" | tr -c 'A-Za-z0-9._-' '_' | cut -c1-128)"
  [[ -n "$value" ]] || value='session'
  [[ "$value" =~ ^[A-Za-z0-9] ]] || value="s-$value"
  value="${value:0:128}"
  knowledge_sanitize_session "$value"
}

knowledge_sanitize_task() {
  knowledge_sanitize_identifier "${1:-}" || knowledge_fail 'task must be a safe filename identifier of at most 128 characters'
}

knowledge_is_stratum_root() {
  local root="${1:-}"
  [[ -d "$root" ]] || return 1
  [[ -f "$root/docs/agent/knowledge-deposition.md" ]] || return 1
  [[ -f "$root/go.mod" ]] || return 1
  [[ "$(sed -n '1{s/[[:space:]]*$//;p;}' "$root/go.mod")" == 'module github.com/ArchChuan/Stratum' ]]
}

knowledge_current_path() {
  local root="$1" client="$2" session="$3"
  printf '%s/tmp/knowledge-deposition/current/%s-%s.json\n' "$root" "$client" "$session"
}

knowledge_report_directory() {
  local root="$1" date="$2"
  printf '%s/tmp/knowledge-deposition/%s\n' "$root" "$date"
}

knowledge_report_basename() {
  local client="$1" session="$2" task="$3"
  printf '%s-%s-%s\n' "$client" "$session" "$task"
}

knowledge_path_within_root() {
  local root="$1" path="$2" relative current component
  [[ "$path" == "$root" || "$path" == "$root/"* ]] || return 1
  [[ "$(realpath -m "$path")" == "$root" || "$(realpath -m "$path")" == "$root/"* ]] || return 1
  relative="${path#"$root"}"
  relative="${relative#/}"
  current="$root"
  IFS='/' read -r -a components <<<"$relative"
  for component in "${components[@]}"; do
    [[ -n "$component" ]] || continue
    current="$current/$component"
    [[ ! -L "$current" ]] || return 1
  done
}

knowledge_validate_normalize() {
  jq -eS '
    def keys_exact($allowed):
      ((keys_unsorted - $allowed) | length) == 0;
    def nonempty: type == "string" and length > 0;
    def string_array: type == "array" and all(.[]; nonempty);
    def forbidden_key:
      [paths(objects) as $p | (getpath($p) | keys_unsorted[]) |
        ascii_downcase |
        select(. == "transcript" or . == "prompt" or . == "password" or
          . == "secret" or . == "token" or . == "api_key" or . == "raw_response")] |
      length > 0;
    def safe_evidence_path:
      nonempty and startswith("/") == false and
      (split("/") | all(.[]; . != ".." and . != ""));
    def persisted_text_valid:
      . as $text |
      (($text | length) <= 8192) and
      (($text | test("[[:cntrl:]]")) | not) and
      (($text | test("(?i)bearer[[:space:]]+[A-Za-z0-9._~+/-]{12,}")) | not) and
      (($text | test("(^|[^A-Za-z0-9])sk-[A-Za-z0-9_-]{20,}")) | not) and
      (($text | test("[A-Za-z0-9_-]{8,}\\.[A-Za-z0-9_-]{8,}\\.[A-Za-z0-9_-]{8,}")) | not) and
      (($text | test("(?i)(token|api[_-]?key)[[:space:]]*[:=][[:space:]]*[^[:space:]]{12,}")) | not);
    def evidence_valid:
      type == "array" and length > 0 and all(.[];
        type == "object" and keys_exact(["path", "anchor"]) and
        (.path | safe_evidence_path) and
        ((has("anchor") | not) or (.anchor | nonempty))
      );
    def candidate_keys:
      ["id", "claim", "destination", "evidence", "scope", "exclusions",
       "duplicate_result", "target", "confidence", "claim_group", "consumption_purpose",
       "knowledge_type", "vault_queries", "related_notes", "verification_status", "governance_action"];
    def candidate_valid:
      . as $candidate |
      type == "object" and keys_exact(candidate_keys) and
      (.id | nonempty and test("^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")) and
      (.claim | nonempty) and
      (.destination == "skill" or .destination == "hook" or .destination == "global_md" or
       .destination == "obsidian" or .destination == "project_git") and
      (.evidence | evidence_valid) and
      (.scope | nonempty) and
      (.exclusions | string_array) and
      (.duplicate_result | nonempty) and
      (.target | nonempty) and
      (.confidence == "low" or .confidence == "medium" or .confidence == "high") and
      ((has("claim_group") | not) or (.claim_group | nonempty)) and
      ((has("consumption_purpose") | not) or (.consumption_purpose | nonempty)) and
      (if .destination == "obsidian" then
        (.knowledge_type == "fact" or .knowledge_type == "principle" or .knowledge_type == "case" or
         .knowledge_type == "counterexample" or .knowledge_type == "correction") and
        (.vault_queries | string_array and length > 0) and
        (.related_notes | string_array and length > 0) and
        (.verification_status | nonempty) and
        (.governance_action == "create" or .governance_action == "merge" or
         .governance_action == "correct" or .governance_action == "queue")
      else
        (["knowledge_type", "vault_queries", "related_notes", "verification_status", "governance_action"] |
          all(.[]; . as $key | ($candidate | has($key) | not)))
      end);
    def claim_groups_valid:
      [.candidates[] | select(has("claim_group"))] |
      group_by(.claim_group) |
      all(.[];
        ([.[].destination] | unique | length) <= 1 or
        (all(.[]; has("consumption_purpose") and (.consumption_purpose | nonempty)) and
         ([.[].consumption_purpose] | unique | length) == length)
      );
    if forbidden_key then error("forbidden key") else . end |
    select([.. | strings] | all(.[]; persisted_text_valid)) |
    select(type == "object") |
    select(keys_exact(["schema_version", "client", "session_id", "task_id", "repository", "created_at",
      "decision", "task_summary", "none_reason", "candidates"])) |
    select(.schema_version == 1) |
    select(.client == "codex" or .client == "claude") |
    select(.session_id | nonempty) |
    select(.task_id | nonempty) |
    select(.repository | type == "object" and keys_exact(["root", "commit"]) and
      (.root | nonempty and startswith("/")) and (.commit | test("^[0-9a-f]{40,64}$"))) |
    select(.created_at | nonempty and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$")) |
    select(.task_summary | nonempty) |
    select(.candidates | type == "array" and all(.[]; candidate_valid)) |
    select(
      if .decision == "none" then
        (.none_reason | nonempty) and (.candidates | length == 0)
      elif .decision == "candidates" then
        .none_reason == null and (.candidates | length > 0)
      else false end
    ) |
    select(claim_groups_valid)
  '
}

knowledge_validate_marker() {
  jq -eS \
    --arg client "$2" --arg session "$3" --arg root "$4" '
    type == "object" and
    keys == ["client","created_at","repository","schema_version","session_id","task_id"] and
    .schema_version == 1 and .client == $client and .session_id == $session and
    (.task_id | type == "string" and test("^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")) and
    .repository == {root:$root} and
    (.created_at | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))
  ' "$1"
}

knowledge_prepare_private_lock() {
  local root="$1" lock_dir lock_path path owner mode
  lock_dir="$root/tmp/knowledge-deposition/.lock"
  lock_path="$lock_dir/report.lock"
  umask 077
  for path in "$root/tmp" "$root/tmp/knowledge-deposition" "$root/tmp/knowledge-deposition/current" "$lock_dir" "$lock_path"; do
    knowledge_path_within_root "$root" "$path" || return 1
  done
  mkdir -p "$root/tmp/knowledge-deposition/current" "$lock_dir" || return 1
  for path in "$root/tmp/knowledge-deposition" "$root/tmp/knowledge-deposition/current" "$lock_dir"; do
    [[ ! -L "$path" && -d "$path" ]] || return 1
    owner="$(stat -Lc '%u' -- "$path")" || return 1
    [[ "$owner" == "$(id -u)" ]] || return 1
    mode="$(stat -Lc '%a' -- "$path")" || return 1
    if (( (8#$mode & 8#077) != 0 )); then chmod 700 -- "$path" || return 1; fi
  done
  if [[ -L "$lock_path" || ( -e "$lock_path" && ! -f "$lock_path" ) ]]; then return 1; fi
  if [[ ! -e "$lock_path" ]]; then
    (set -o noclobber; : >"$lock_path") 2>/dev/null || [[ ! -L "$lock_path" && -f "$lock_path" ]] || return 1
  fi
  knowledge_open_private_lock "$root"
}

knowledge_open_private_lock() {
  local root="$1" lock_dir lock_path path owner mode
  lock_dir="$root/tmp/knowledge-deposition/.lock"
  lock_path="$lock_dir/report.lock"
  for path in "$root/tmp" "$root/tmp/knowledge-deposition" "$root/tmp/knowledge-deposition/current" "$lock_dir" "$lock_path"; do
    knowledge_path_within_root "$root" "$path" || return 1
  done
  for path in "$root/tmp/knowledge-deposition" "$root/tmp/knowledge-deposition/current" "$lock_dir"; do
    [[ ! -L "$path" && -d "$path" ]] || return 1
    owner="$(stat -Lc '%u' -- "$path")" || return 1
    [[ "$owner" == "$(id -u)" ]] || return 1
    mode="$(stat -Lc '%a' -- "$path")" || return 1
    [[ "$mode" == '700' ]] || return 1
  done
  [[ ! -L "$lock_path" && -f "$lock_path" ]] || return 1
  [[ "$(stat -Lc '%u' -- "$lock_path")" == "$(id -u)" ]] || return 1
  exec 9<"$lock_path"
  [[ "$(realpath -e /proc/self/fd/9)" == "$lock_path" ]] || return 1
  [[ -f /proc/self/fd/9 && ! -L "$lock_path" && -f "$lock_path" ]] || return 1
  [[ "$(stat -Lc '%d:%i:%u' -- /proc/self/fd/9)" == "$(stat -Lc '%d:%i:%u' -- "$lock_path")" ]] || return 1
  flock 9
  knowledge_path_within_root "$root" "$lock_dir"
}
