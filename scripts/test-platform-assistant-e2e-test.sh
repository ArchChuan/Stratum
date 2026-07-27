#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
script="$root/scripts/test-platform-assistant-e2e.sh"
workflow="$root/.github/workflows/ci.yml"

require() {
  local file="$1" pattern="$2" description="$3"
  if ! grep -Eq -- "$pattern" "$file"; then
    printf 'platform assistant E2E contract missing: %s\n' "$description" >&2
    exit 1
  fi
}

[[ -x "$script" ]] || { printf 'platform assistant E2E script is missing or not executable\n' >&2; exit 1; }
require "$script" 'mktemp -d' 'unique ephemeral workspace'
require "$script" 'command -v docker' 'missing Docker fails closed'
require "$script" 'docker info' 'unavailable Docker daemon fails closed'
require "$script" '127\.0\.0\.1::5432' 'Docker-assigned localhost PostgreSQL port'
require "$script" 'pg_isready' 'PostgreSQL readiness gate'
require "$script" 'REQUIRE_PLATFORM_ASSISTANT_E2E=1' 'fail-closed E2E requirement flag'
require "$script" 'STRATUM_TEST_POSTGRES_URL=' 'isolated PostgreSQL DSN export'
require "$script" 'trap .*cleanup.*(EXIT|0)' 'trap-based exact cleanup'
require "$script" 'docker rm -f -- "\$container"' 'exact container cleanup'
require "$script" 'PostgresAuthorizationSecretsAndConcurrency' 'existing proposal PostgreSQL suite'
require "$script" 'RealServices' 'real service PostgreSQL suite'

require "$workflow" '^  platform-assistant-e2e:' 'blocking CI job'
require "$workflow" 'Run platform assistant PostgreSQL E2E' 'CI invokes the platform assistant E2E gate'
require "$workflow" 'bash scripts/test-platform-assistant-e2e\.sh' 'CI uses the canonical script'
require "$workflow" 'needs:.*platform-assistant-e2e' 'build waits for platform assistant E2E'

printf 'platform assistant E2E contract tests passed\n'
