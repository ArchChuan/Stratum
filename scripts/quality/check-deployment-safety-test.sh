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
OPIK_VALUES="${ROOT}/helm/opik/values-demo.yaml"
OPIK_COLLECTOR="${ROOT}/k8s/opik-otel-collector.yaml"
PLATFORM_ASSISTANT_REMOTE_VERIFY="${ROOT}/scripts/e2e/platform-assistant-remote-verify.sh"

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
require 'opik-2\.1\.32\.tgz' 'versioned Opik Helm chart artifact'
require 'sha256sum --check' 'Opik Helm chart checksum verification'
require 'helm upgrade --install opik /tmp/opik-2\.1\.32\.tgz' 'verified local Opik chart installation'
require_file "${OPIK_COLLECTOR}" 'opentelemetry-collector-contrib@sha256:[0-9a-f]{64}' \
    'collector image digest pin'
require 'opik-backend\.opik\.svc\.cluster\.local:8080' 'in-cluster Opik API URL'
require 'Apply Opik OTLP collector' 'collector applied before Stratum release'
require 'rollout status deployment/opik-backend' 'Opik backend readiness wait'
reject 'continue-on-error:[[:space:]]*true' 'deployment errors are not suppressed'

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
require_file "${REMOTE_HTTP_VALUES}" 'router\.entrypoints:[[:space:]]*"web,web2"' \
    'remote HTTP profile uses the Traefik web and public web2 entrypoints'
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
require 'kubectl get service traefik -n kube-system -o json' 'Traefik service port mapping diagnostics'
require 'kubectl get deployment traefik -n kube-system -o json' 'Traefik entrypoint argument diagnostics'
require 'svccontroller\.k3s\.cattle\.io/svcname=traefik' 'Traefik ServiceLB diagnostics'
require 'kubectl port-forward service/stratum-frontend 18080:80' 'internal frontend verification tunnel'
require 'http://127\.0\.0\.1:18080/api/health' 'internal frontend health verification'
require_file "${POSTGRES_DOCKERFILE}" 'curl .*--connect-timeout[[:space:]]+[0-9]+' \
    'SCWS download connection timeout'
require_file "${POSTGRES_DOCKERFILE}" 'curl .*--max-time[[:space:]]+[0-9]+' 'SCWS download total timeout'
require_file "${POSTGRES_DOCKERFILE}" 'curl .*--retry[[:space:]]+[1-9][0-9]*' 'SCWS download finite retries'
require_file "${POSTGRES_DOCKERFILE}" 'curl .*--retry-all-errors' 'SCWS download retry classification'
if [[ ! -f "${OPIK_VALUES}" || ! -f "${OPIK_COLLECTOR}" ]]; then
    echo 'deployment safety contract missing: pinned Opik values or collector manifest' >&2
    exit 1
fi
require_file "${OPIK_VALUES}" 'replicaCount:[[:space:]]*1' 'single-node Opik replicas'
require_file "${OPIK_VALUES}" 'persistence:' 'Opik persistent storage'
require_file "${OPIK_VALUES}" 'resources:' 'Opik resource limits'
require_file "${OPIK_COLLECTOR}" 'receivers:' 'collector OTLP receiver'
require_file "${OPIK_COLLECTOR}" 'otlphttp/opik:' 'collector Opik exporter'
require_file "${OPIK_COLLECTOR}" 'otlp/jaeger:' 'collector Jaeger exporter'
require_file "${OPIK_COLLECTOR}" 'exporters:[[:space:]]*\[otlp/jaeger,[[:space:]]*otlphttp/opik\]' \
    'collector fan-out to Jaeger and Opik'
require_file "${OPIK_COLLECTOR}" 'filter/drop_probes:' 'collector probe trace filter'
for route in readyz livez metrics; do
    require_file "${OPIK_COLLECTOR}" "http\.route.*\\\"/${route}\\\"" "collector drops /${route} traces"
done
require_file "${OPIK_COLLECTOR}" 'file_storage/queue:' 'collector persistent queue storage'
require_file "${OPIK_COLLECTOR}" 'storage:[[:space:]]*file_storage/queue' \
    'collector exporters use persistent queues'
require_file "${OPIK_COLLECTOR}" 'claimName:[[:space:]]*opik-otel-collector-queue' \
    'collector queue uses persistent volume storage'
require_file "${OPIK_COLLECTOR}" 'health_check:' 'collector readiness health check'
require_file "${OPIK_COLLECTOR}" 'image:[[:space:]]*otel/opentelemetry-collector-contrib@sha256:[0-9a-f]{64}' \
    'collector image is digest pinned'

for values_file in "${DEMO_VALUES}" "${DEMO_LOCAL_VALUES}"; do
    for key in opikUrl opikProject opikWorkspace tracePayloadEnabled tracePayloadEndpoint tracePayloadBucket tracePayloadUseTls; do
        if ! grep -Eq "^[[:space:]]+${key}:" "${values_file}"; then
            echo "deployment safety contract missing: ${key} in ${values_file}" >&2
            exit 1
        fi
    done
done

require_file "${DEMO_VALUES}" 'opikUrl:[[:space:]]*"http://opik-backend\.opik\.svc\.cluster\.local:8080"' \
    'Demo uses the in-cluster Opik API'

if [[ ! -f "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" ]]; then
    echo 'deployment safety contract missing: platform assistant remote verifier' >&2
    exit 1
fi
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" '/api/health' 'remote public health check'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'GUEST_ATTEMPTS=5' 'five bounded guest login attempts'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" '/api/agents/stratum-platform-assistant' \
    'managed Agent member read check'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'systemPrompt' 'managed Agent prompt omission check'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" '/api/agents/executions' 'member execution diagnostics check'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'deployment/opik-backend' 'Opik backend readiness check'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'deployment/opik-otel-collector' 'OTEL collector readiness check'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'baseline_projection' 'proposal baseline column check'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'edit_count' 'proposal edit count column check'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'GROUP BY table_schema' \
    'proposal columns checked per tenant schema'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" '/api/auth/me' 'administrator bearer identity check'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" "role IN \('owner',[[:space:]]*'admin'\)" \
    'aggregate tenant administrator count'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" "settings->'llm_api_keys'" \
    'aggregate configured provider count'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'stratum_diagnose_tenant' \
    'configured chain diagnostic tool evidence'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'diagnosticReport' \
    'configured chain structured diagnostic report'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'X-Request-ID' \
    'configured chain request trace correlation'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'trace_id' \
    'configured chain Opik execution correlation'
for state in passed failed prerequisite_missing; do
    require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" "${state}" "configured chain ${state} state"
done
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'exit 1' 'failed remote verification exits nonzero'
reject_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'echo.*(token|password|api[_-]?key)' \
    'remote verifier prints a credential'

if [[ -e "${ROOT}/.github/workflows/mirror.yml" ]]; then
    echo 'deployment safety contract violated: Gitee mirror workflow still exists' >&2
    exit 1
fi

if git -C "${ROOT}" grep -in gitee -- .github docs/deployment >/dev/null 2>&1; then
    echo 'deployment safety contract violated: Gitee references remain' >&2
    exit 1
fi

echo 'deployment safety contract tests passed'
