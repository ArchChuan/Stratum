#!/usr/bin/env bash
# 保守策略清理已废弃 worktree。
#
# 判定删除（满足任一，且工作区必须干净）：
#   1. 分支已从远端删除（gone）且本地分支已并入 origin/main
#   2. 分支远端仍在，但 origin/<branch> 已并入 origin/main（PR 合并后远端分支未删）
#   3. 分支已 gone，且其 PR 已 merged（squash/rebased 合并场景，HEAD 不是 main 祖先）
# 其余一律只报告不删除，避免丢失未推送的工作。
# gh 缺失或查询失败时场景 3 fail closed 跳过。
# 日志追加到 ~/.claude/stratum-worktree-cleanup.log，并刷新 ~/.claude/.last-cleanup。
#
# 用法: scripts/cleanup-stale-worktrees.sh [--dry-run] [--keep <path>]
#   --dry-run  只预览不删除
#   --keep     显式指定不删除的 worktree（默认取脚本进程的 $PWD，
#               交互运行建议直接放在当前会话 worktree 内执行）

set -euo pipefail

# crontab 环境 PATH 最小化，兜底补齐
export PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

log_file="$HOME/.claude/stratum-worktree-cleanup.log"

dry_run=0
keep="$PWD"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) dry_run=1 ;;
    --keep) keep="${2:?--keep 需要路径参数}"; shift ;;
    *) printf 'error: 未知参数: %s\n' "$1" >&2; exit 2 ;;
  esac
  shift
done

# 其他工具管理的运行时 worktree，生命周期由对应工具控制，排除在本脚本之外
exclude_prefixes=(
  "$HOME/.local/share/feishu-agent-bridge"
  "$HOME/.local/state/feishu-agent-bridge"
)

log() {
  printf '[%s] %s\n' "$(date -Iseconds)" "$*" >>"$log_file"
  printf '%s\n' "$*"
}

git rev-parse --is-inside-work-tree >/dev/null 2>&1 || {
  printf 'error: run this command inside a Git worktree\n' >&2
  exit 1
}

# 先与远端同步，删除远端已消失分支的本地 ref，作为 gone 判断依据
git fetch --prune --no-tags origin

# squash/rebased 合并场景：分支 HEAD 不是 origin/main 祖先，但 PR 已 merged。
# gh 缺失或查询失败时置空 → 该场景 fail closed 跳过，不误删。
merged_prs=""
if command -v gh >/dev/null 2>&1; then
  merged_prs=$(gh pr list --state merged --limit 300 --json headRefName 2>/dev/null | jq -r '.[].headRefName' 2>/dev/null || true)
  if [[ -z "$merged_prs" ]]; then
    log "warn: gh PR merged 列表为空或查询失败，squash 场景判定跳过"
  fi
else
  log "warn: gh 不可用，squash 场景判定跳过"
fi

removed=0
skipped=0

while IFS= read -r wt; do
  [[ -n "$wt" ]] || continue

  if [[ ! -d "$wt" ]]; then
    log "skip: $wt (目录不存在)"
    skipped=$((skipped + 1))
    continue
  fi

  excluded=0
  for prefix in "${exclude_prefixes[@]}"; do
    if [[ "$wt" == "$prefix"* ]]; then
      log "skip: $wt (外部工具管理的运行时 worktree)"
      skipped=$((skipped + 1))
      excluded=1
      break
    fi
  done
  [[ $excluded -eq 1 ]] && continue

  # 跳过 --keep 指定的 worktree（交互运行时保留当前会话，crontab 运行时保留主仓库）
  if [[ "$wt" == "$keep" ]]; then
    log "skip: $wt (--keep 指定保留)"
    skipped=$((skipped + 1))
    continue
  fi

  branch=$(git -C "$wt" rev-parse --abbrev-ref HEAD 2>/dev/null || true)
  if [[ -z "$branch" || "$branch" == "HEAD" ]]; then
    log "skip: $wt (detached HEAD)"
    skipped=$((skipped + 1))
    continue
  fi
  case "$branch" in
    main | master)
      log "skip: $wt (默认分支 $branch)"
      skipped=$((skipped + 1))
      continue
      ;;
  esac

  if [[ -n "$(git -C "$wt" status --porcelain 2>/dev/null || true)" ]]; then
    log "skip: $wt (工作区有未提交改动)"
    skipped=$((skipped + 1))
    continue
  fi

  gone=0
  if ! git show-ref --verify --quiet "refs/remotes/origin/$branch"; then
    gone=1
  fi

  reason=""
  if [[ $gone -eq 0 ]]; then
    # 远端分支仍在：若已并入 main（PR 合后远端分支未删），本地 worktree 废弃
    if git merge-base --is-ancestor "refs/remotes/origin/$branch" refs/remotes/origin/main 2>/dev/null; then
      reason="分支 $branch 远端仍在但已并入 main"
    else
      log "skip: $wt (分支 $branch 仍在远端且未并入 main)"
      skipped=$((skipped + 1))
      continue
    fi
  elif git merge-base --is-ancestor "$branch" refs/remotes/origin/main 2>/dev/null; then
    reason="分支 $branch 已并入 main"
  elif [[ -n "$merged_prs" ]] && grep -qxF "$branch" <<<"$merged_prs"; then
    reason="分支 $branch 的 PR 已 merged (squash)"
  else
    log "skip: $wt (分支 $branch 有未合并提交且 PR 未 merged)"
    skipped=$((skipped + 1))
    continue
  fi

  if [[ $dry_run -eq 1 ]]; then
    log "dry-run: 将删除 $wt ($reason)"
  else
    git worktree remove "$wt"
    git branch -D "$branch"
    log "deleted: $wt ($reason)"
    removed=$((removed + 1))
  fi
done < <(git worktree list --porcelain | awk '/^worktree /{print $2}')

log "done: removed=$removed skipped=$skipped dry_run=$dry_run"
date -Iseconds >"$HOME/.claude/.last-cleanup"
