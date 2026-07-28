#!/usr/bin/env bash
set -euo pipefail
umask 077

usage() {
  printf 'Usage: %s --repo-root ABS\n' "$(basename "$0")" >&2
  exit 2
}

fail() {
  printf 'knowledge-deposition hook uninstall failed: %s\n' "$*" >&2
  exit 1
}

[[ "$#" -eq 2 && "$1" == "--repo-root" ]] || usage
repo_root="$2"
[[ "$repo_root" == /* ]] || fail "--repo-root must be absolute"
[[ -d "$repo_root" && ! -L "$repo_root" ]] || fail "repository root is not a safe directory"
repo_root="$(realpath -e "$repo_root")" || fail "repository root cannot be resolved"
git_top="$(git -C "$repo_root" rev-parse --show-toplevel 2>/dev/null)" || fail "repository root is not a Git worktree"
git_dir="$(git -C "$repo_root" rev-parse --absolute-git-dir 2>/dev/null)" || fail "repository Git directory is unavailable"
git_common_dir="$(git -C "$repo_root" rev-parse --path-format=absolute --git-common-dir 2>/dev/null)" || \
  fail "repository common Git directory is unavailable"
[[ "$(realpath -e "$git_top")" == "$repo_root" && "$(realpath -e "$git_dir")" == "$(realpath -e "$git_common_dir")" ]] || \
  fail "repository root must be the primary Stratum checkout"
[[ -f "$repo_root/go.mod" ]] || fail "repository root is not Stratum"
grep -Fxq 'module github.com/byteBuilderX/stratum' "$repo_root/go.mod" || fail "repository root is not Stratum"

codex_config="${CODEX_HOOKS_JSON:-${HOME:?HOME is required}/.codex/hooks.json}"
claude_config="${CLAUDE_SETTINGS_JSON:-${HOME:?HOME is required}/.claude/settings.json}"

safe_config() {
  local path="$1" parent component current
  [[ "$path" == /* ]] || fail "config path must be absolute"
  [[ -f "$path" && ! -L "$path" ]] || fail "config must be an existing regular non-symlink file: $path"
  parent="$(dirname "$path")"
  current="/"
  IFS='/' read -r -a components <<<"${parent#/}"
  for component in "${components[@]}"; do
    [[ -n "$component" ]] || continue
    current="${current%/}/$component"
    [[ -d "$current" && ! -L "$current" ]] || fail "config path contains an unsafe parent: $current"
  done
}

validate_config() {
  local path="$1"
  jq -e '
    type == "object" and
    ((.hooks // {}) | type == "object") and
    ((.hooks.UserPromptSubmit // []) | type == "array") and
    ((.hooks.Stop // []) | type == "array") and
    all((.hooks.UserPromptSubmit // [])[];
      type == "object" and ((.hooks // []) | type == "array") and all((.hooks // [])[]; type == "object")) and
    all((.hooks.Stop // [])[];
      type == "object" and ((.hooks // []) | type == "array") and all((.hooks // [])[]; type == "object"))
  ' "$path" >/dev/null 2>&1 || fail "invalid hooks JSON shape: $path"
}

safe_config "$codex_config"
safe_config "$claude_config"
[[ "$codex_config" != "$claude_config" ]] || fail "Codex and Claude config paths must be distinct"

codex_lock="${codex_config}.knowledge-deposition.lock"
claude_lock="${claude_config}.knowledge-deposition.lock"
lock_paths=("$codex_lock" "$claude_lock")
if [[ "${lock_paths[0]}" > "${lock_paths[1]}" ]]; then
  lock_paths=("${lock_paths[1]}" "${lock_paths[0]}")
fi
for lock_path in "${lock_paths[@]}"; do
  [[ ! -L "$lock_path" && ( ! -e "$lock_path" || -f "$lock_path" ) ]] || fail "unsafe uninstaller lock: $lock_path"
done
exec {lock_fd_one}>"${lock_paths[0]}" || fail "could not open uninstaller lock"
chmod 600 "${lock_paths[0]}" || fail "could not protect uninstaller lock"
flock -x "$lock_fd_one" || fail "could not acquire uninstaller lock"
exec {lock_fd_two}>"${lock_paths[1]}" || fail "could not open uninstaller lock"
chmod 600 "${lock_paths[1]}" || fail "could not protect uninstaller lock"
flock -x "$lock_fd_two" || fail "could not acquire uninstaller lock"

validate_config "$codex_config"
validate_config "$claude_config"
codex_digest="$(sha256sum "$codex_config" | awk '{print $1}')" || fail "could not hash Codex hooks"
claude_digest="$(sha256sum "$claude_config" | awk '{print $1}')" || fail "could not hash Claude settings"

codex_tmp="$(mktemp "$(dirname "$codex_config")/.hooks.json.knowledge-deposition.XXXXXX")"
claude_tmp="$(mktemp "$(dirname "$claude_config")/.settings.json.knowledge-deposition.XXXXXX")"
cleanup() {
  [[ -z "$codex_tmp" ]] || rm -f "$codex_tmp"
  [[ -z "$claude_tmp" ]] || rm -f "$claude_tmp"
}
trap cleanup EXIT
chmod 600 "$codex_tmp" "$claude_tmp"

transform() {
  local source="$1" destination="$2"
  jq '
    def managed:
      (.command | type == "string") and
      (.command | test(
        "^bash /(?:\\\\.|[A-Za-z0-9_@%+=:,./-])*/scripts/knowledge-deposition/" +
        "(?:codex|claude)-(?:task-start|stop)\\.sh$"
      ));
    def clean_event:
      map(
        if has("hooks") then
          .hooks = (.hooks | map(select(managed | not))) |
          select((.hooks | length) > 0)
        else
          .
        end
      );
    .hooks = (.hooks // {}) |
    .hooks.UserPromptSubmit = ((.hooks.UserPromptSubmit // []) | clean_event) |
    .hooks.Stop = ((.hooks.Stop // []) | clean_event)
  ' "$source" >"$destination"
}

transform "$codex_config" "$codex_tmp"
transform "$claude_config" "$claude_tmp"
validate_config "$codex_tmp"
validate_config "$claude_tmp"

if cmp -s "$codex_config" "$codex_tmp" && cmp -s "$claude_config" "$claude_tmp"; then
  printf 'Knowledge deposition hooks already absent; no changes made.\n'
  exit 0
fi

timestamp="$(date -u +%Y%m%dT%H%M%S%N)-$$"
codex_backup="${codex_config}.knowledge-deposition.${timestamp}.bak"
claude_backup="${claude_config}.knowledge-deposition.${timestamp}.bak"
[[ ! -e "$codex_backup" && ! -e "$claude_backup" ]] || fail "backup collision"
[[ "$(sha256sum "$codex_config" | awk '{print $1}')" == "$codex_digest" && \
   "$(sha256sum "$claude_config" | awk '{print $1}')" == "$claude_digest" ]] || \
  fail "configuration changed during uninstall; no changes made"
if ! cp -- "$codex_config" "$codex_backup"; then
  rm -f "$codex_backup" "$claude_backup"
  fail "could not back up Codex hooks; no changes made"
fi
if ! cp -- "$claude_config" "$claude_backup"; then
  rm -f "$codex_backup" "$claude_backup"
  fail "could not back up Claude settings; no changes made"
fi
if ! chmod 600 "$codex_backup" "$claude_backup"; then
  rm -f "$codex_backup" "$claude_backup"
  fail "could not protect configuration backups; no changes made"
fi

if ! mv -- "$codex_tmp" "$codex_config"; then
  fail "could not replace Codex hooks; originals remain in place"
fi
codex_tmp=""
if ! mv -- "$claude_tmp" "$claude_config"; then
  if ! mv -- "$codex_backup" "$codex_config"; then
    fail "could not replace Claude settings and could not restore Codex hooks; backup: $codex_backup"
  fi
  fail "could not replace Claude settings; restored Codex hooks; Claude original preserved"
fi
claude_tmp=""
printf 'Removed knowledge deposition hooks. Backups: %s %s\n' "$codex_backup" "$claude_backup"
