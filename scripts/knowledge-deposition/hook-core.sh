#!/usr/bin/env bash

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "$SCRIPT_DIR/common.sh"

knowledge_quiet_allow() { jq -cn '{continue:true,suppressOutput:true}'; }
knowledge_block() { jq -cn --arg reason "$1" '{decision:"block",reason:$reason,continue:false,suppressOutput:false}'; }

knowledge_payload_fields() {
  jq -er 'select(type == "object") | [.cwd,.session_id] | select(all(.[]; type == "string" and length > 0)) | @tsv' 2>/dev/null
}

knowledge_malformed_response() {
  local input="$1" cwd root
  cwd="$(jq -er 'select(type == "object") | .cwd | select(type == "string" and length > 0)' <<<"$input" 2>/dev/null)" || {
    knowledge_quiet_allow
    return
  }
  root="$(knowledge_resolve_root "$cwd")" || { knowledge_quiet_allow; return; }
  knowledge_block "knowledge deposition: malformed hook payload for Stratum repository $root"
}

knowledge_resolve_root() {
  local cwd="$1" root
  [[ -d "$cwd" ]] || return 1
  root="$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null)" || return 1
  root="$(realpath -e "$root")" || return 1
  knowledge_is_stratum_root "$root" || return 1
  printf '%s\n' "$root"
}

knowledge_start() {
  local client="$1" envelope="$2" input fields cwd raw_session root session task created marker tmp command message
  input="$(cat)"
  fields="$(printf '%s' "$input" | knowledge_payload_fields)" || {
    knowledge_malformed_response "$input"
    return
  }
  IFS=$'\t' read -r cwd raw_session <<<"$fields"
  root="$(knowledge_resolve_root "$cwd")" || { knowledge_quiet_allow; return; }
  session="$(knowledge_normalize_session "$raw_session")" || { knowledge_block 'knowledge deposition: invalid session identifier'; return; }
  task="task-$(date -u +'%s%N')-$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')"
  created="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  marker="$(knowledge_current_path "$root" "$client" "$session")"
  knowledge_prepare_private_lock "$root" || { knowledge_block 'knowledge deposition: unsafe private marker or lock path'; return; }
  knowledge_path_within_root "$root" "$marker" || { knowledge_block 'knowledge deposition: current marker path is unsafe'; return; }
  [[ ! -L "$marker" && ( ! -e "$marker" || -f "$marker" ) ]] || { knowledge_block 'knowledge deposition: current marker is unsafe'; return; }
  tmp="$(mktemp "$(dirname "$marker")/.marker.XXXXXX")" || { knowledge_block 'knowledge deposition: marker staging failed'; return; }
  jq -cn --arg client "$client" --arg session "$session" --arg task "$task" --arg root "$root" --arg created "$created" \
    '{schema_version:1,client:$client,session_id:$session,task_id:$task,repository:{root:$root},created_at:$created}' >"$tmp"
  chmod 600 "$tmp"
  mv -f -- "$tmp" "$marker"
  command="bash scripts/knowledge-deposition/report.sh --client $client --session $session --task $task --repo-root $root"
  message="Knowledge deposition task gate active.\nClient: $client\nSession: $session\nTask: $task\nBefore stopping, submit the report JSON to:\n$command"
  if [[ "$envelope" == codex ]]; then
    jq -cn --arg message "$message" '{continue:true,systemMessage:$message}'
  else
    jq -cn --arg message "$message" '{continue:true,hookSpecificOutput:{hookEventName:"UserPromptSubmit",additionalContext:$message}}'
  fi
}

knowledge_stop() {
  local client="$1" input fields cwd raw_session root session reason
  input="$(cat)"
  fields="$(printf '%s' "$input" | knowledge_payload_fields)" || { knowledge_malformed_response "$input"; return; }
  IFS=$'\t' read -r cwd raw_session <<<"$fields"
  root="$(knowledge_resolve_root "$cwd")" || { knowledge_quiet_allow; return; }
  session="$(knowledge_normalize_session "$raw_session")" || { knowledge_block 'knowledge deposition: invalid session identifier'; return; }
  if reason="$($SCRIPT_DIR/check.sh --client "$client" --session "$session" --repo-root "$root" 2>&1)"; then
    knowledge_quiet_allow
  else
    knowledge_block "$reason"
  fi
}
