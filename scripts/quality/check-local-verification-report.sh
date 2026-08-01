#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
report=${LOCAL_VERIFY_REPORT_PATH:-$root/tmp/test-verification/local-verification.json}
source_root=${LOCAL_VERIFY_SOURCE_ROOT:-$root}
manifest=${LOCAL_VERIFY_MANIFEST_PATH:-$source_root/.test/verification.yaml}
schema=${LOCAL_VERIFY_SCHEMA_PATH:-$root/.test/schemas/local-verification.schema.json}

source_is_clean() {
  local changed
  changed=$(git -C "$source_root" status --porcelain --untracked-files=all | awk '
    {path=substr($0,4)}
    path !~ /^test\/e2e\/attestations\// {print path}
  ')
  [[ -z "$changed" ]]
}

mark_stale() {
  local temporary
  temporary=$(mktemp "$(dirname "$report")/.local-verification.XXXXXX")
  jq '.status = "stale" | .source_clean = false' "$report" >"$temporary"
  mv "$temporary" "$report"
  go run "$root/cmd/verification-schema" --schema "$schema" --input "$report" >/dev/null
}

[[ -f "$report" ]] || { printf 'local verification report is missing: %s\n' "$report" >&2; exit 1; }
[[ -f "$manifest" ]] || { printf 'verification manifest is missing: %s\n' "$manifest" >&2; exit 1; }
go run "$root/cmd/verification-schema" --schema "$schema" --input "$report" >/dev/null
head=$(git -C "$source_root" rev-parse HEAD)
digest=sha256:$(sha256sum "$manifest" | awk '{print $1}')
fresh=$(jq -e --arg head "$head" --arg digest "$digest" '
  .status == "passed" and .tested_commit == $head and .manifest_digest == $digest and .source_clean
' "$report" >/dev/null && printf true || printf false)
if [[ "$fresh" != true ]] || ! source_is_clean; then
  jq -e '.status == "passed"' "$report" >/dev/null 2>&1 && mark_stale
  printf 'local verification report is stale or not passed\n' >&2
  exit 1
fi
printf 'local verification report is fresh\n'
