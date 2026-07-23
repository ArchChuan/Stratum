#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/common.sh"

usage() { knowledge_fail 'usage: check.sh --client codex|claude --session SAFE --repo-root ABSOLUTE'; }
[[ $# -eq 6 ]] || { usage; exit 1; }
client='' session='' repo_root=''
while [[ $# -gt 0 ]]; do
  flag="$1"; value="$2"; shift 2
  case "$flag" in
    --client) [[ -z "$client" ]] || { usage; exit 1; }; client="$value" ;;
    --session) [[ -z "$session" ]] || { usage; exit 1; }; session="$value" ;;
    --repo-root) [[ -z "$repo_root" ]] || { usage; exit 1; }; repo_root="$value" ;;
    *) usage; exit 1 ;;
  esac
done
client="$(knowledge_safe_client "$client")" || exit 1
session="$(knowledge_sanitize_session "$session")" || exit 1
[[ "$repo_root" = /* ]] || { knowledge_fail 'repo root must be absolute'; exit 1; }
repo_root="$(realpath -e "$repo_root")" || { knowledge_fail 'repo root does not exist'; exit 1; }
knowledge_is_stratum_root "$repo_root" || { knowledge_fail 'repo root is not Stratum'; exit 1; }
marker="$(knowledge_current_path "$repo_root" "$client" "$session")"
fallback="bash scripts/knowledge-deposition/report.sh --client $client --session $session --task TASK --repo-root $repo_root"
knowledge_path_within_root "$repo_root" "$marker" || { knowledge_fail "current marker path is unsafe; run: $fallback"; exit 1; }
[[ ! -L "$marker" && -f "$marker" ]] || { knowledge_fail "current task marker is missing or unsafe; run: $fallback"; exit 1; }
knowledge_validate_marker "$marker" "$client" "$session" "$repo_root" >/dev/null 2>&1 || {
  knowledge_fail "current task marker is malformed; run: $fallback"; exit 1;
}
task="$(jq -r '.task_id' "$marker")"
command="bash scripts/knowledge-deposition/report.sh --client $client --session $session --task $task --repo-root $repo_root"
basename="$(knowledge_report_basename "$client" "$session" "$task")"
mapfile -d '' -t reports < <(find "$repo_root/tmp/knowledge-deposition" -mindepth 2 -maxdepth 2 -type f -name "$basename.json" ! -path '*/current/*' -print0 2>/dev/null)
[[ "${#reports[@]}" -eq 1 ]] || { knowledge_fail "exact report is missing or ambiguous; run: $command"; exit 1; }
json_path="${reports[0]}"; markdown_path="${json_path%.json}.md"
knowledge_path_within_root "$repo_root" "$json_path" && knowledge_path_within_root "$repo_root" "$markdown_path" || {
  knowledge_fail "report path is unsafe; run: $command"; exit 1;
}
[[ ! -L "$json_path" && -f "$json_path" ]] || { knowledge_fail "report JSON is missing or unsafe; run: $command"; exit 1; }
[[ ! -L "$markdown_path" && -f "$markdown_path" ]] || { knowledge_fail "paired Markdown report is missing or unsafe; run: $command"; exit 1; }
normalized="$(mktemp)"; trap 'rm -f "$normalized"' EXIT
knowledge_validate_normalize <"$json_path" >"$normalized" 2>/dev/null || { knowledge_fail "report JSON is malformed; run: $command"; exit 1; }
commit="$(git -C "$repo_root" rev-parse --verify HEAD 2>/dev/null)" || { knowledge_fail "repository commit is unavailable; run: $command"; exit 1; }
jq -e --arg client "$client" --arg session "$session" --arg task "$task" --arg root "$repo_root" --arg commit "$commit" \
  '.client == $client and .session_id == $session and .task_id == $task and .repository.root == $root and .repository.commit == $commit' \
  "$normalized" >/dev/null || { knowledge_fail "report identity or commit does not match the current task; run: $command"; exit 1; }
