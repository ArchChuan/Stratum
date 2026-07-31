#!/usr/bin/env bash
set -euo pipefail

review_type=${1:?review type is required}
output=${2:?output path is required}
case "$review_type" in specification|code-quality|release-evidence) ;; *) exit 2 ;; esac
commit=${GITHUB_SHA:?GITHUB_SHA is required}
run_id=${GITHUB_RUN_ID:?GITHUB_RUN_ID is required}
repository=${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}
environment=${REVIEW_ENVIRONMENT:?REVIEW_ENVIRONMENT is required}
mkdir -p "$(dirname "$output")"
jq -n --arg type "$review_type" --arg commit "$commit" \
  --arg reviewer "github-environment:$environment" \
  --arg evidence "https://github.com/$repository/actions/runs/$run_id" \
  '{type:$type,status:"passed",reviewer:$reviewer,commit:$commit,policy_version:1,findings:[],evidence:$evidence}' >"$output"
