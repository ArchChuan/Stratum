#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORKFLOW="${ROOT}/.github/workflows/deploy.yml"

require() {
    local pattern="$1" description="$2"
    if ! grep -Eq -- "${pattern}" "${WORKFLOW}"; then
        echo "deployment safety contract missing: ${description}" >&2
        exit 1
    fi
}

reject() {
    local pattern="$1" description="$2"
    if grep -Eq -- "${pattern}" "${WORKFLOW}"; then
        echo "deployment safety contract violated: ${description}" >&2
        exit 1
    fi
}

require 'group:[[:space:]]*stratum-production' 'fixed production concurrency group'
require 'cancel-in-progress:[[:space:]]*false' 'non-cancelling active deployment'
require 'Verify deployment candidate' 'stale main SHA gate'
require 'api\.github\.com/repos/.*/commits/main' 'fail-closed current main lookup'
require 'sha256:\[0-9a-f\]\{64\}' 'registry digest validation'
require '--set-string app\.image\.digest=' 'backend digest deployment'
require '--set-string frontend\.image\.digest=' 'frontend digest deployment'

for component in database redis nats etcd minio milvus; do
    require "--set-string ${component}\\.image\\.digest=" "${component} digest deployment"
done

require 'metrics-server/releases/download/v[0-9]+\.[0-9]+\.[0-9]+/components\.yaml' \
    'version-pinned metrics-server manifest'
reject 'minio/minio:latest|/minio:latest' 'mutable MinIO latest tag'
reject 'metrics-server/releases/latest' 'mutable metrics-server latest manifest'
reject '\|\|[[:space:]]*true' 'suppressed deployment errors'

if [[ -e "${ROOT}/.github/workflows/mirror.yml" ]]; then
    echo 'deployment safety contract violated: Gitee mirror workflow still exists' >&2
    exit 1
fi

if git -C "${ROOT}" grep -in gitee -- .github docs/deployment >/dev/null 2>&1; then
    echo 'deployment safety contract violated: Gitee references remain' >&2
    exit 1
fi

echo 'deployment safety contract tests passed'
