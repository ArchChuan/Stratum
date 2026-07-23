#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "$SCRIPT_DIR/common.sh"

usage() {
  knowledge_fail 'usage: report.sh --client codex|claude --session SAFE --task SAFE --repo-root ABSOLUTE'
}

[[ $# -eq 8 ]] || { usage; exit 1; }

client=''
session=''
task=''
repo_root=''
while [[ $# -gt 0 ]]; do
  flag="$1"
  [[ $# -ge 2 ]] || { usage; exit 1; }
  value="$2"
  shift 2
  case "$flag" in
    --client) [[ -z "$client" ]] || { usage; exit 1; }; client="$value" ;;
    --session) [[ -z "$session" ]] || { usage; exit 1; }; session="$value" ;;
    --task) [[ -z "$task" ]] || { usage; exit 1; }; task="$value" ;;
    --repo-root) [[ -z "$repo_root" ]] || { usage; exit 1; }; repo_root="$value" ;;
    *) usage; exit 1 ;;
  esac
done

client="$(knowledge_safe_client "$client")" || exit 1
session="$(knowledge_sanitize_session "$session")" || exit 1
task="$(knowledge_sanitize_task "$task")" || exit 1
[[ "$repo_root" = /* ]] || { knowledge_fail 'repo root must be absolute'; exit 1; }
repo_root="$(realpath -e "$repo_root")" || { knowledge_fail 'repo root does not exist'; exit 1; }
knowledge_is_stratum_root "$repo_root" || { knowledge_fail 'repo root is not Stratum'; exit 1; }

commit="$(git -C "$repo_root" rev-parse --verify HEAD 2>/dev/null)" || {
  knowledge_fail 'repository commit is unavailable'
  exit 1
}
created_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
report_date="${created_at%%T*}"
report_dir="$(knowledge_report_directory "$repo_root" "$report_date")"
basename="$(knowledge_report_basename "$client" "$session" "$task")"
json_path="$report_dir/$basename.json"
markdown_path="$report_dir/$basename.md"
knowledge_root="$repo_root/tmp/knowledge-deposition"
current_path="$(knowledge_current_path "$repo_root" "$client" "$session")"
lock_dir="$knowledge_root/.lock"
lock_path="$lock_dir/report.lock"
latest_path="$knowledge_root/latest.md"

ensure_private_owned_directory() {
  local path="$1" current_uid owner_uid mode
  [[ ! -L "$path" && -d "$path" ]] || return 1
  current_uid="$(id -u)"
  owner_uid="$(stat -Lc '%u' -- "$path")" || return 1
  [[ "$owner_uid" == "$current_uid" ]] || return 1
  mode="$(stat -Lc '%a' -- "$path")" || return 1
  if (( (8#$mode & 8#077) != 0 )); then
    chmod 700 -- "$path" || return 1
    mode="$(stat -Lc '%a' -- "$path")" || return 1
  fi
  [[ "$mode" == '700' ]]
}

for boundary_path in "$repo_root/tmp" "$knowledge_root" "$(dirname "$current_path")" "$current_path" \
  "$lock_dir" "$lock_path" "$report_dir" "$json_path" "$markdown_path" "$latest_path"; do
  knowledge_path_within_root "$repo_root" "$boundary_path" || {
    knowledge_fail 'repository report path contains a symlink or escapes the repository'
    exit 1
  }
done

mkdir -p "$lock_dir"
knowledge_path_within_root "$repo_root" "$lock_dir" || {
  knowledge_fail 'lock directory is unsafe'
  exit 1
}
ensure_private_owned_directory "$knowledge_root" || {
  knowledge_fail 'knowledge report directory must be a private owned directory'
  exit 1
}
ensure_private_owned_directory "$lock_dir" || {
  knowledge_fail 'lock directory must be a private owned directory'
  exit 1
}
if [[ -L "$lock_path" || ( -e "$lock_path" && ! -f "$lock_path" ) ]]; then
  knowledge_fail 'lock file is unsafe'
  exit 1
fi
if [[ ! -e "$lock_path" ]]; then
  if ! (set -o noclobber; : >"$lock_path") 2>/dev/null; then
    [[ ! -L "$lock_path" && -f "$lock_path" ]] || {
      knowledge_fail 'lock file could not be created safely'
      exit 1
    }
  fi
fi
[[ ! -L "$lock_path" && -f "$lock_path" ]] || { knowledge_fail 'lock file changed'; exit 1; }
[[ "$(stat -Lc '%u' -- "$lock_path")" == "$(id -u)" ]] || { knowledge_fail 'lock file owner is unsafe'; exit 1; }

# Threat model: reject caller-controlled or preexisting symlink escapes and pin the validated lock inode.
# A process with the same Unix UID can mutate repository paths and is not a separate security principal.
exec 9<"$lock_path"
lock_fd_path="$(realpath -e "/proc/self/fd/9")" || { knowledge_fail 'lock file descriptor is unsafe'; exit 1; }
[[ "$lock_fd_path" == "$lock_path" ]] || { knowledge_fail 'lock file descriptor escaped'; exit 1; }
[[ -f "/proc/self/fd/9" && ! -L "$lock_path" && -f "$lock_path" ]] || {
  knowledge_fail 'lock file descriptor changed'
  exit 1
}
[[ "$(stat -Lc '%d:%i:%u' -- /proc/self/fd/9)" == "$(stat -Lc '%d:%i:%u' -- "$lock_path")" ]] || {
  knowledge_fail 'lock file descriptor does not match lock path'
  exit 1
}
flock 9
knowledge_path_within_root "$repo_root" "$lock_dir" || { knowledge_fail 'lock directory changed'; exit 1; }

knowledge_path_within_root "$repo_root" "$current_path" || {
  knowledge_fail 'current task marker path is unsafe'
  exit 1
}
marker_task="$(jq -er 'select(type == "object") | select(keys == ["task_id"]) | .task_id | select(type == "string")' \
  "$current_path" 2>/dev/null)" || { knowledge_fail 'current task marker is invalid'; exit 1; }
[[ "$marker_task" == "$task" ]] || { knowledge_fail 'current task marker does not match task'; exit 1; }

mkdir -p "$report_dir"
knowledge_path_within_root "$repo_root" "$report_dir" || { knowledge_fail 'report directory is unsafe'; exit 1; }
normalized_tmp="$(mktemp "$report_dir/.normalized.XXXXXX")"
markdown_tmp=''
latest_tmp=''
cleanup() {
  rm -f "$normalized_tmp"
  [[ -z "$markdown_tmp" ]] || rm -f "$markdown_tmp"
  [[ -z "$latest_tmp" ]] || rm -f "$latest_tmp"
}
trap cleanup EXIT HUP INT TERM

if ! jq -es \
  --arg client "$client" \
  --arg session "$session" \
  --arg task "$task" \
  --arg root "$repo_root" \
  --arg commit "$commit" \
  --arg created "$created_at" \
  'if length == 1 and (.[0] | type == "object") then
    .[0] + {
      schema_version: 1,
      client: $client,
      session_id: $session,
      task_id: $task,
      repository: {root: $root, commit: $commit},
      created_at: $created
    }
  else error("expected exactly one JSON object") end' 2>/dev/null |
  knowledge_validate_normalize >"$normalized_tmp" 2>/dev/null; then
  knowledge_fail 'report JSON failed validation'
  exit 1
fi

knowledge_validate_normalize <"$normalized_tmp" >/dev/null 2>&1 || {
  knowledge_fail 'normalized report failed validation'
  exit 1
}

markdown_tmp="$(mktemp "$report_dir/.markdown.XXXXXX")"
jq -r '
  def md:
    explode |
    map(. as $char |
      if ([92, 96, 42, 95, 123, 125, 91, 93, 40, 41, 35, 43, 33, 124, 62, 60] | index($char))
      then [92, $char] else [$char] end) |
    add | implode;
  def evidence:
    .evidence[] | "  - Evidence: " + (((.path + (if has("anchor") then "#" + .anchor else "" end)) | md));
  [
    "# Knowledge deposition report",
    "",
    "- Client: `" + .client + "`",
    "- Session: `" + .session_id + "`",
    "- Task: `" + .task_id + "`",
    "- Repository: " + (.repository.root | md),
    "- Commit: `" + .repository.commit + "`",
    "- Created: `" + .created_at + "`",
    "- Decision: `" + .decision + "`",
    "",
    "## Task summary",
    "",
    "Summary: " + (.task_summary | md),
    ""
  ] +
  (if .decision == "none" then
    ["## No candidates", "", "Reason: " + (.none_reason | md), ""]
  else
    ["## Candidates", ""] +
    ([.candidates[] |
      [
        "### " + (.id | md),
        "",
        "Claim: " + (.claim | md),
        "",
        "- Destination: `" + .destination + "`",
        "- Target: " + (.target | md),
        "- Scope: " + (.scope | md),
        "- Confidence: `" + .confidence + "`",
        "- Duplicate result: " + (.duplicate_result | md)
      ] +
      ([evidence]) +
      ["- Exclusions/counterexamples: " + ((.exclusions | join("; ")) | md)] +
      (if has("claim_group") then ["- Claim group: " + (.claim_group | md)] else [] end) +
      (if has("consumption_purpose") then ["- Consumption purpose: " + (.consumption_purpose | md)] else [] end) +
      (if .destination == "obsidian" then [
        "- Knowledge type: `" + .knowledge_type + "`",
        "- Vault queries: " + ((.vault_queries | join("; ")) | md),
        "- Related notes: " + ((.related_notes | join("; ")) | md),
        "- Verification status: " + (.verification_status | md),
        "- Governance action: `" + .governance_action + "`"
      ] else [] end) + [""]
    ] | add)
  end) | .[]
' "$normalized_tmp" >"$markdown_tmp"

if [[ -e "$json_path" || -L "$json_path" || -e "$markdown_path" || -L "$markdown_path" ]]; then
  knowledge_fail 'report destination already exists'
  exit 1
fi

mv "$normalized_tmp" "$json_path"
normalized_tmp=''
if [[ "${KNOWLEDGE_REPORT_TEST_FAIL_SECOND_PUBLISH:-}" == '1' ]] || ! mv "$markdown_tmp" "$markdown_path"; then
  rm -f "$json_path" || knowledge_fail 'failed to roll back partial report publication'
  knowledge_fail 'failed to publish report pair'
  exit 1
fi
markdown_tmp=''

latest_tmp="$(mktemp "$repo_root/tmp/knowledge-deposition/.latest.XXXXXX")"
{
  printf '# Latest knowledge deposition reports\n'
  find "$repo_root/tmp/knowledge-deposition" -mindepth 2 -type f \
    \( -name '*.json' -o -name '*.md' \) \
    ! -path "$repo_root/tmp/knowledge-deposition/current/*" -print0 |
    sort -z |
    while IFS= read -r -d '' path; do
      relative="${path#"$repo_root/tmp/knowledge-deposition/"}"
      printf -- '- [%s](%s)\n' "$relative" "$relative"
    done
} >"$latest_tmp"
mv -f "$latest_tmp" "$latest_path"
latest_tmp=''

printf '%s\n' "$markdown_path"
