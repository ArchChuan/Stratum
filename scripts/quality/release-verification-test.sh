#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
workflow=$root/.github/workflows/deploy.yml

fail() {
  printf 'release verification contract: %s\n' "$1" >&2
  exit 1
}

require() {
  grep -Eq -- "$1" "$workflow" || fail "$2"
}

require '^  candidate:' 'candidate job is missing'
require 'github\.event\.workflow_run\.head_sha' 'workflow_run head SHA is not used'
require 'github\.event\.workflow_run\.conclusion' 'workflow_run success is not checked'
require 'api\.github\.com/repos/.*/commits/main' 'current main comparison is missing'
require 'sha:[[:space:]]*\$\{\{[[:space:]]*needs\.candidate\.outputs\.sha[[:space:]]*\}\}' \
  'candidate SHA is not propagated through build fan-in'
require 'ref:[[:space:]]*\$\{\{[[:space:]]*needs\.candidate\.outputs\.sha[[:space:]]*\}\}' \
  'checkouts are not pinned to the candidate SHA'
require 'release-verification\.schema\.json' 'release receipt is not schema validated'
require 'migration_check:\$migration' 'release receipt does not record migration status'
require 'health_check:\$health' 'release receipt does not record health status'
require 'rollback_check:\$rollback' 'release receipt does not record rollback status'
require 'prior_digests:' 'release receipt does not record prior digests'
require 'rollback_basis:\$rollback_basis' 'release receipt does not record rollback basis'

if grep -Eq 'type=raw,value=\$\{\{[[:space:]]*github\.sha|tags:.*\$\{\{[[:space:]]*github\.sha' "$workflow"; then
  fail 'image tags still use the workflow file SHA'
fi
if grep -Eq -- '--arg commit "\$GITHUB_SHA"' "$workflow"; then
  fail 'release receipt still records the workflow file SHA'
fi

checkout_count=$(grep -c 'uses: actions/checkout@v4' "$workflow")
pinned_count=$(grep -c 'ref: \${{ needs.candidate.outputs.sha }}' "$workflow")
[[ "$checkout_count" -eq "$pinned_count" ]] || fail 'not every checkout is pinned to the candidate SHA'

printf 'release verification workflow contract passed\n'
