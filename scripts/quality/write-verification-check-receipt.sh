#!/usr/bin/env bash
set -euo pipefail

plan=${1:?verification plan is required}
output=${2:?output path is required}
shift 2
(( $# > 0 )) || { printf 'executed check IDs are required\n' >&2; exit 1; }
commit=${GITHUB_SHA:?GITHUB_SHA is required}
run_id=${GITHUB_RUN_ID:?GITHUB_RUN_ID is required}
repository=${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}
[[ -f "$plan" ]] || { printf 'verification plan is missing: %s\n' "$plan" >&2; exit 1; }
jq -e --arg commit "$commit" '.commit == $commit and (.checks | length > 0)' "$plan" >/dev/null
executed=$(jq -cn '$ARGS.positional | sort | unique' --args -- "$@")
planned=$(jq -cS '.checks | sort | unique' "$plan")
[[ "$executed" == "$planned" ]] || { printf 'executed checks do not match verification plan\n' >&2; exit 1; }
mkdir -p "$(dirname "$output")"
jq --arg evidence "https://github.com/$repository/actions/runs/$run_id" --argjson executed "$executed" '
  {version:1,commit:.commit,manifest_digest:.manifest_digest,issuer:"github-actions",
   results:[$executed[] | {id:.,status:"passed",evidence:$evidence}]}' "$plan" >"$output"
