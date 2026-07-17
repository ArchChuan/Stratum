#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  printf 'Usage: %s <worktree-path> <branch>\n' "$0" >&2
  exit 2
fi

path=$1
branch=$2

git rev-parse --is-inside-work-tree >/dev/null 2>&1 || {
  printf 'error: run this command inside a Git worktree\n' >&2
  exit 1
}
git remote get-url origin >/dev/null 2>&1 || {
  printf 'error: origin remote is required\n' >&2
  exit 1
}
git check-ref-format --branch "$branch" >/dev/null
if [[ "$branch" == main || "$branch" == master ]]; then
  printf 'error: worktree branch must not be %s\n' "$branch" >&2
  exit 1
fi
if git show-ref --verify --quiet "refs/heads/$branch"; then
  printf 'error: branch already exists: %s\n' "$branch" >&2
  exit 1
fi
if [[ -e "$path" ]]; then
  printf 'error: worktree path already exists: %s\n' "$path" >&2
  exit 1
fi

git fetch --no-tags origin main:refs/remotes/origin/main
git worktree add "$path" -b "$branch" origin/main
