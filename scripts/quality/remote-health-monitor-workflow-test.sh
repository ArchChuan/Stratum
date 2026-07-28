#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORKFLOW="${ROOT}/.github/workflows/remote-health-monitor.yml"

require() {
    local pattern="$1" description="$2"
    if ! grep -Eq -- "${pattern}" "${WORKFLOW}"; then
        echo "remote health workflow contract missing: ${description}" >&2
        exit 1
    fi
}

reject() {
    local pattern="$1" description="$2"
    if grep -Eq -- "${pattern}" "${WORKFLOW}"; then
        echo "remote health workflow contract violated: ${description}" >&2
        exit 1
    fi
}

require "cron:[[:space:]]*['\"]\\*/5 \\* \\* \\* \\*['\"]" 'five-minute schedule'
require 'workflow_dispatch:' 'manual dispatch'
require 'contents:[[:space:]]*read' 'read-only contents permission'
require 'issues:[[:space:]]*write' 'issue write permission'
require 'group:[[:space:]]*remote-health-monitor' 'fixed concurrency group'
require 'cancel-in-progress:[[:space:]]*false' 'non-cancelling execution'
require 'timeout-minutes:[[:space:]]*3' 'three-minute job timeout'
require "go-version:[[:space:]]*['\"]1\\.25\\.12['\"]" 'repository Go version'
require 'REMOTE_HEALTH_URL:[[:space:]]*\$\{\{ vars\.REMOTE_HEALTH_URL \}\}' 'health URL variable wiring'
require 'FEISHU_WEBHOOK_URL:[[:space:]]*\$\{\{ secrets\.FEISHU_WEBHOOK_URL \}\}' 'Feishu secret wiring'
require 'GITHUB_TOKEN:[[:space:]]*\$\{\{ github\.token \}\}' 'GitHub token wiring'
require 'GITHUB_REPOSITORY:[[:space:]]*\$\{\{ github\.repository \}\}' 'repository identity wiring'
require 'go run ./cmd/remote-health-monitor' 'monitor execution'
reject 'continue-on-error:[[:space:]]*true' 'failure suppression'
reject 'curl|wget' 'unsafe parallel HTTP implementation'
