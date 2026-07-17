#!/usr/bin/env bash
set -euo pipefail

project_root=$(cd "$(dirname "$0")/.." && pwd)
create_script="$project_root/scripts/new-worktree.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

git init --bare --initial-branch=main "$tmp/origin.git" >/dev/null
git clone "$tmp/origin.git" "$tmp/stale" >/dev/null 2>&1
git -C "$tmp/stale" config user.name test
git -C "$tmp/stale" config user.email test@example.com
printf 'initial\n' >"$tmp/stale/state.txt"
git -C "$tmp/stale" add state.txt
git -C "$tmp/stale" commit -m initial >/dev/null
git -C "$tmp/stale" push origin main >/dev/null

git clone "$tmp/origin.git" "$tmp/updater" >/dev/null 2>&1
git -C "$tmp/updater" config user.name test
git -C "$tmp/updater" config user.email test@example.com
printf 'updated\n' >"$tmp/updater/state.txt"
git -C "$tmp/updater" commit -am update >/dev/null
git -C "$tmp/updater" push origin main >/dev/null

stale_head=$(git -C "$tmp/stale" rev-parse HEAD)
remote_head=$(git -C "$tmp/updater" rev-parse HEAD)
[[ "$stale_head" != "$remote_head" ]]

(
  cd "$tmp/stale"
  bash "$create_script" "$tmp/worktree" feat/fresh-main
)

worktree_head=$(git -C "$tmp/worktree" rev-parse HEAD)
origin_main=$(git -C "$tmp/stale" rev-parse refs/remotes/origin/main)
[[ "$worktree_head" == "$remote_head" ]]
[[ "$worktree_head" == "$origin_main" ]]

printf 'PASS: new worktree starts at latest origin/main (%s)\n' "$worktree_head"
