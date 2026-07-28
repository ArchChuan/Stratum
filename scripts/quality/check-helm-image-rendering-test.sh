#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "${TMP_ROOT}"' EXIT

TAG_RENDER="${TMP_ROOT}/tag.yaml"
DIGEST_RENDER="${TMP_ROOT}/digest.yaml"
REMOTE_HTTP_RENDER="${TMP_ROOT}/remote-http.yaml"
REMOTE_HTTP_INGRESS="${TMP_ROOT}/remote-http-ingress.yaml"
SERVICE_MONITOR_RENDER="${TMP_ROOT}/service-monitor.yaml"
LOCAL_RENDER="${TMP_ROOT}/local.yaml"
REMOTE_HTTP_VALUES="${ROOT}/helm/values-demo-remote-http.yaml"
OPIK_COLLECTOR="${ROOT}/k8s/opik-otel-collector.yaml"

if [[ ! -f "${REMOTE_HTTP_VALUES}" ]]; then
    echo 'remote HTTP Helm values are missing' >&2
    exit 1
fi

helm template stratum "${ROOT}/helm" -f "${ROOT}/helm/values-demo.yaml" >"${TAG_RENDER}"
grep -Fq 'registry.cn-hangzhou.aliyuncs.com/stratum-demo/stratum-backend:demo' "${TAG_RENDER}"

helm template stratum "${ROOT}/helm" -f "${ROOT}/helm/values-demo.yaml" | \
    awk '/# Source: stratum\/templates\/servicemonitor.yaml/{found=1} found{print}' >"${SERVICE_MONITOR_RENDER}"
grep -Fq 'kind: ServiceMonitor' "${SERVICE_MONITOR_RENDER}"
grep -Fq 'release: kps' "${SERVICE_MONITOR_RENDER}"
grep -Fq 'app.kubernetes.io/component: backend' "${SERVICE_MONITOR_RENDER}"
grep -Eq 'path:[[:space:]]*"?/metrics"?$' "${SERVICE_MONITOR_RENDER}"
grep -Eq 'interval:[[:space:]]*"?30s"?$' "${SERVICE_MONITOR_RENDER}"
grep -Eq 'scrapeTimeout:[[:space:]]*"?10s"?$' "${SERVICE_MONITOR_RENDER}"

args=()
components=(app frontend database redis nats etcd minio milvus)
for index in "${!components[@]}"; do
    component="${components[$index]}"
    digit="$((index + 1))"
    digest="sha256:$(printf '%064x' "${digit}")"
    args+=(--set-string "${component}.image.digest=${digest}")
done

helm template stratum "${ROOT}/helm" -f "${ROOT}/helm/values-demo.yaml" \
    "${args[@]}" >"${DIGEST_RENDER}"

repositories=(
    registry.cn-hangzhou.aliyuncs.com/stratum-demo/stratum-backend
    registry.cn-hangzhou.aliyuncs.com/stratum-demo/stratum-frontend
    postgres
    redis
    nats
    quay.io/coreos/etcd
    minio/minio
    milvusdb/milvus
)

for index in "${!repositories[@]}"; do
    digest="sha256:$(printf '%064x' "$((index + 1))")"
    grep -Fq "${repositories[$index]}@${digest}" "${DIGEST_RENDER}"
done

helm template stratum "${ROOT}/helm" \
    -f "${ROOT}/helm/values-demo.yaml" \
    -f "${REMOTE_HTTP_VALUES}" \
    --set-string config.frontendUrl=http://203.0.113.10:6879 \
    --set-string config.githubCallbackUrl=http://203.0.113.10:6879/api/auth/github/callback \
    >"${REMOTE_HTTP_RENDER}"

grep -Fq 'FRONTEND_URL: "http://203.0.113.10:6879"' "${REMOTE_HTTP_RENDER}"
grep -Fq 'GITHUB_CALLBACK_URL: "http://203.0.113.10:6879/api/auth/github/callback"' "${REMOTE_HTTP_RENDER}"
grep -Fq 'SECURE_COOKIES: "false"' "${REMOTE_HTTP_RENDER}"
grep -Fq 'OPIK_URL: "http://opik-backend.opik.svc.cluster.local:8080"' "${REMOTE_HTTP_RENDER}"
grep -Fq 'ENVIRONMENT: "production"' "${REMOTE_HTTP_RENDER}"
grep -Fq 'GIN_MODE: "release"' "${REMOTE_HTTP_RENDER}"
grep -Fq 'name: APP_ENV' "${REMOTE_HTTP_RENDER}"

helm template stratum "${ROOT}/helm" \
    -f "${ROOT}/helm/values-demo.yaml" \
    -f "${ROOT}/helm/values-demo-local.yaml" >"${LOCAL_RENDER}"
grep -Fq 'ENVIRONMENT: "demo"' "${LOCAL_RENDER}"
grep -Fq 'GIN_MODE: "debug"' "${LOCAL_RENDER}"

if ! grep -Eq 'image:[[:space:]]*otel/opentelemetry-collector-contrib@sha256:[0-9a-f]{64}' \
    "${OPIK_COLLECTOR}"; then
    echo 'collector image is not digest pinned' >&2
    exit 1
fi
grep -Fq 'otlphttp/opik:' "${OPIK_COLLECTOR}"
grep -Fq 'opik-backend.opik.svc.cluster.local:8080/v1/private/otel' "${OPIK_COLLECTOR}"

awk '/^kind: Ingress$/{found=1} found{print} found && /^---$/{exit}' \
    "${REMOTE_HTTP_RENDER}" >"${REMOTE_HTTP_INGRESS}"
grep -Eq 'traefik\.ingress\.kubernetes\.io/router\.entrypoints:[[:space:]]*"?web,web2"?$' \
    "${REMOTE_HTTP_INGRESS}"
if grep -Eq '^[[:space:]]+(host|tls):' "${REMOTE_HTTP_INGRESS}"; then
    echo 'remote HTTP Ingress unexpectedly contains a host or TLS section' >&2
    exit 1
fi

echo 'Helm image rendering tests passed'
