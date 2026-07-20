#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORKFLOW="${ROOT}/.github/workflows/deploy.yml"
CI_WORKFLOW="${ROOT}/.github/workflows/ci.yml"
HELM_DEPLOYMENT="${ROOT}/helm/templates/deployment.yaml"
PROD_VALUES="${ROOT}/helm/values-prod.yaml"
DEMO_VALUES="${ROOT}/helm/values-demo.yaml"
DEMO_LOCAL_VALUES="${ROOT}/helm/values-demo-local.yaml"

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
reject 'StrictHostKeyChecking=no' 'disabled SSH host verification'
reject 'insecure-skip-tls-verify|certificate-authority-data:/d' 'disabled Kubernetes API verification'

if grep -Eq 'gosec@latest|gosec .*\|\|[[:space:]]*true' "${CI_WORKFLOW}"; then
    echo 'deployment safety contract violated: security scanner is unpinned or non-blocking' >&2
    exit 1
fi
if ! grep -Eq 'gosec@v2\.25\.0' "${CI_WORKFLOW}"; then
    echo 'deployment safety contract violated: gosec version is not compatible with the CI Go toolchain' >&2
    exit 1
fi
if ! grep -Eq 'COVERAGE_TARGET:[[:space:]]*"80"' "${CI_WORKFLOW}" ||
    ! grep -Eq 'COVERAGE_BASELINE:[[:space:]]*"38\.0"' "${CI_WORKFLOW}" ||
    ! grep -Eq '::error::Coverage .*below enforced baseline' "${CI_WORKFLOW}" ||
    ! grep -Eq '^[[:space:]]*exit 1' "${CI_WORKFLOW}"; then
    echo 'deployment safety contract missing: enforced coverage baseline and explicit target' >&2
    exit 1
fi
if grep -Eq 'sslmode=disable' "${HELM_DEPLOYMENT}" "${PROD_VALUES}"; then
    echo 'deployment safety contract violated: production PostgreSQL TLS disabled' >&2
    exit 1
fi
if ! grep -Eq 'checksum/secret:' "${HELM_DEPLOYMENT}"; then
    echo 'deployment safety contract missing: Secret rollout checksum' >&2
    exit 1
fi
if ! grep -Eq 'secrets\.externalChecksum=' "${WORKFLOW}" ||
    ! grep -Eq 'kubectl get secret .*sha256sum' "${WORKFLOW}"; then
    echo 'deployment safety contract missing: external Secret rollout checksum' >&2
    exit 1
fi
if grep -Eh 'frontendUrl:[[:space:]]*"http://|githubCallbackUrl:[[:space:]]*"http://' \
    "${DEMO_VALUES}" "${DEMO_LOCAL_VALUES}" | grep -Ev '"http://localhost([:/"]|$)' >/dev/null; then
    echo 'deployment safety contract violated: remote demo authentication uses HTTP' >&2
    exit 1
fi

if [[ -e "${ROOT}/.github/workflows/mirror.yml" ]]; then
    echo 'deployment safety contract violated: Gitee mirror workflow still exists' >&2
    exit 1
fi

if git -C "${ROOT}" grep -in gitee -- .github docs/deployment >/dev/null 2>&1; then
    echo 'deployment safety contract violated: Gitee references remain' >&2
    exit 1
fi

echo 'deployment safety contract tests passed'
