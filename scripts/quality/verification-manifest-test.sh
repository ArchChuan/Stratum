#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
manifest="${root}/.test/verification.yaml"

fail() { printf 'verification manifest: %s\n' "$1" >&2; exit 1; }
require() {
  local pattern="$1" label="$2"
  grep -Eq -- "$pattern" "$manifest" || fail "missing ${label}"
}

[[ -f "$manifest" ]] || fail '.test/verification.yaml'
require '^version:[[:space:]]*1$' 'schema version'
require '^  authority:[[:space:]]*ci$' 'CI authority'
require '^  fail_closed:[[:space:]]*true$' 'fail-closed policy'
require '^  default_mode:[[:space:]]*(short|soak)$' 'default mode'
require 'rerun_for_diagnostics:[[:space:]]*true' 'diagnostic-only reruns'
require 'quarantine_requires_owner:[[:space:]]*true' 'quarantine owner requirement'
require 'skipped_allowed:[[:space:]]*false' 'fail-closed skipped capability policy'
require 'unreconciled_allowed:[[:space:]]*false' 'fail-closed unreconciled capability policy'
require 'levels:[[:space:]]*\[R0, R1, R2, R3, R4\]' 'risk levels'
for level in R0 R1 R2 R3 R4; do require "^  ${level}:" "${level} checks"; done
awk '
  /^    - id:/ { if (seen && !level) exit 1; seen=1; level=0 }
  /^      level: R[0-4]$/ { level=1 }
  END { if (seen && !level) exit 1 }
' "$manifest" || fail 'risk rule missing valid R0-R4 level'
require '^    - id: tenant-boundary$' 'tenant risk rule'
require '^    - id: agent-tool-chain$' 'agent/MCP risk rule'
require '^    - id: auth$' 'authentication risk rule'
require '^    - id: migration$' 'migration risk rule'
require '^    - id: memory$' 'Memory risk rule'
require '^    - id: external-dependency$' 'external dependency risk rule'
require '^    - id: deployment$' 'deployment risk rule'
require 'id: platform-assistant' 'Platform Assistant capability'
require 'specification-review' 'specification review check'
require 'code-quality-review' 'code quality review check'
require '^  schema:[[:space:]]*2$' 'attestation schema v2'
require 'manifest_digest' 'manifest digest binding'
require 'artifact_digests' 'artifact digest binding'

printf 'verification manifest contract passed\n'
