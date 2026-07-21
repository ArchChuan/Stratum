#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORKFLOW="${ROOT}/.github/workflows/deploy.yml"
CI_WORKFLOW="${ROOT}/.github/workflows/ci.yml"
HELM_DEPLOYMENT="${ROOT}/helm/templates/deployment.yaml"
PROD_VALUES="${ROOT}/helm/values-prod.yaml"
DEMO_VALUES="${ROOT}/helm/values-demo.yaml"
DEMO_LOCAL_VALUES="${ROOT}/helm/values-demo-local.yaml"
REMOTE_HTTP_VALUES="${ROOT}/helm/values-demo-remote-http.yaml"
POSTGRES_DOCKERFILE="${ROOT}/docker/postgres-zhparser.Dockerfile"

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

require_file() {
    local file="$1" pattern="$2" description="$3"
    if ! grep -Eq -- "${pattern}" "${file}"; then
        echo "deployment safety contract missing: ${description}" >&2
        exit 1
    fi
}

reject_file() {
    local file="$1" pattern="$2" description="$3"
    if grep -Eq -- "${pattern}" "${file}"; then
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
require_file "${DEMO_VALUES}" 'frontendUrl:[[:space:]]*"https://' 'HTTPS demo frontend URL'
require_file "${DEMO_VALUES}" 'githubCallbackUrl:[[:space:]]*"https://' 'HTTPS demo OAuth callback URL'
require_file "${DEMO_VALUES}" 'secureCookies:[[:space:]]*"true"' 'HTTPS demo secure cookies'
require_file "${DEMO_VALUES}" 'router\.entrypoints:[[:space:]]*"websecure"' 'HTTPS demo secure entrypoint'
require_file "${DEMO_VALUES}" '^[[:space:]]+tls:' 'HTTPS demo TLS configuration'

require_file "${DEMO_LOCAL_VALUES}" 'frontendUrl:[[:space:]]*"http://localhost([:/"]|$)' \
    'localhost-only demo frontend URL'
require_file "${DEMO_LOCAL_VALUES}" 'githubCallbackUrl:[[:space:]]*"http://localhost([:/"]|$)' \
    'localhost-only demo OAuth callback URL'
reject_file "${DEMO_LOCAL_VALUES}" 'http://([0-9]{1,3}\.){3}[0-9]{1,3}' \
    'local demo contains a remote IP URL'

require_file "${REMOTE_HTTP_VALUES}" 'secureCookies:[[:space:]]*"false"' \
    'remote HTTP profile disables secure cookies'
require_file "${REMOTE_HTTP_VALUES}" 'router\.entrypoints:[[:space:]]*"web"' \
    'remote HTTP profile uses the Traefik web entrypoint'
require_file "${REMOTE_HTTP_VALUES}" 'host:[[:space:]]*""' 'remote HTTP profile uses a hostless Ingress'
require_file "${REMOTE_HTTP_VALUES}" 'tls:[[:space:]]*\[\]' 'remote HTTP profile disables TLS'
reject_file "${REMOTE_HTTP_VALUES}" 'frontendUrl:|githubCallbackUrl:|http://([0-9]{1,3}\.){3}[0-9]{1,3}' \
    'remote HTTP profile hard-codes its public address'

require 'validate-remote-http-base-url\.sh[[:space:]]+"\$PUBLIC_BASE_URL"' \
    'PUBLIC_BASE_URL validation before deployment'
require '-f[[:space:]]+helm/values-demo-remote-http\.yaml' 'remote HTTP Helm overlay deployment'
require '--set-string[[:space:]]+config\.frontendUrl="\$PUBLIC_BASE_URL"' 'public frontend URL injection'
require '--set-string[[:space:]]+config\.githubCallbackUrl="\$PUBLIC_BASE_URL/api/auth/github/callback"' \
    'public OAuth callback URL injection'
require 'kubectl get ingress -n stratum -o wide' 'deployed Ingress diagnostics'
require 'kubectl get endpoints stratum stratum-frontend -n stratum' 'service endpoint diagnostics'
require 'ss -H -ltnp.*sport = :80.*sport = :443.*sport = :6879' \
    'host HTTP edge listener diagnostics'
require 'http://127\.0\.0\.1/api/health' 'host-local Traefik health diagnostic'
require '--header[[:space:]]+"Host:[[:space:]]*\$PUBLIC_AUTHORITY"[[:space:]]+http://127\.0\.0\.1/api/health' \
    'host-local port 80 public Host diagnostic'
require '--header[[:space:]]+"Host:[[:space:]]*\$PUBLIC_AUTHORITY"[[:space:]]+http://127\.0\.0\.1:6879/api/health' \
    'host-local port 6879 public Host diagnostic'
require 'kubectl get service traefik -n kube-system -o wide' 'Traefik service exposure diagnostics'
require 'svccontroller\.k3s\.cattle\.io/svcname=traefik' 'Traefik ServiceLB diagnostics'
require 'kubectl port-forward service/stratum-frontend 18080:80' 'internal frontend verification tunnel'
require 'http://127\.0\.0\.1:18080/api/health' 'internal frontend health verification'
require_file "${POSTGRES_DOCKERFILE}" 'curl .*--connect-timeout[[:space:]]+[0-9]+' \
    'SCWS download connection timeout'
require_file "${POSTGRES_DOCKERFILE}" 'curl .*--max-time[[:space:]]+[0-9]+' 'SCWS download total timeout'
require_file "${POSTGRES_DOCKERFILE}" 'curl .*--retry[[:space:]]+[1-9][0-9]*' 'SCWS download finite retries'
require_file "${POSTGRES_DOCKERFILE}" 'curl .*--retry-all-errors' 'SCWS download retry classification'

if [[ -e "${ROOT}/.github/workflows/mirror.yml" ]]; then
    echo 'deployment safety contract violated: Gitee mirror workflow still exists' >&2
    exit 1
fi

if git -C "${ROOT}" grep -in gitee -- .github docs/deployment >/dev/null 2>&1; then
    echo 'deployment safety contract violated: Gitee references remain' >&2
    exit 1
fi

echo 'deployment safety contract tests passed'
