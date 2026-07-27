#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
script="$root/scripts/test-platform-assistant-browser-e2e.sh"
config="$root/web/playwright.real.config.ts"
spec="$root/web/e2e/system-assistant-real.spec.ts"
workflow="$root/.github/workflows/ci.yml"

require() {
  local file="$1" pattern="$2" description="$3"
  if [[ ! -f "$file" ]] || ! grep -Eq -- "$pattern" "$file"; then
    printf 'platform assistant browser E2E contract missing: %s\n' "$description" >&2
    exit 1
  fi
}

[[ -x "$script" ]] || {
  printf 'platform assistant browser E2E runner is missing or not executable\n' >&2
  exit 1
}
require "$script" 'mktemp -d' 'isolated runtime directory'
require "$script" 'REAL_PLATFORM_ASSISTANT_E2E=1' 'mandatory real E2E mode'
require "$script" 'platform-assistant-stubs' 'deterministic process-local stub'
require "$script" 'QWEN_BASE_URL=' 'backend routes Qwen to the stub'
require "$script" '/readyz' 'stub or backend bounded readiness'
require "$script" 'playwright\.real\.config\.ts' 'dedicated Playwright config'
require "$script" 'stop_process_group' 'exact child-process cleanup'
require "$config" "name: 'mobile-390'" 'mobile viewport'
require "$config" "name: 'desktop-1440'" 'desktop viewport'
require "$spec" "REAL_PLATFORM_ASSISTANT_E2E" 'spec cannot silently run in mocked mode'
require "$spec" 'page\.screenshot' 'real viewport screenshot capture'
require "$spec" 'canvas' 'canvas-free content check'
require "$spec" 'byteLength' 'non-empty screenshot pixel evidence'
require "$spec" 'sensitiveMarkers' 'screenshot secret-marker boundary check'
if grep -Eq 'page\.route\(' "$spec"; then
  printf 'platform assistant browser E2E must not intercept business APIs\n' >&2
  exit 1
fi
require "$workflow" '^  platform-assistant-browser-e2e:' 'blocking browser CI job'
require "$workflow" 'playwright install --with-deps chromium' 'Chromium setup'
require "$workflow" 'bash scripts/test-platform-assistant-browser-e2e\.sh' 'canonical browser runner invocation'
require "$workflow" 'needs:.*platform-assistant-browser-e2e' 'build waits for browser E2E'

printf 'platform assistant browser E2E contract tests passed\n'
