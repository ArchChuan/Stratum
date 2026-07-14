#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

go test -v -race -timeout 30s ./...
if [[ -f web/package.json ]]; then
  npm --prefix web run lint
  npm --prefix web run build
fi
