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

# fix-provider-keys pre-upgrade hook:image 必须与 deployment 同源渲染,禁止
# `<no value>` 非法引用(db-migration-hook 曾因传根 context 渲染坏镜像而从未
# 生效);secret env 必须指向与 deployment 同一 secret。
FIX_HOOK="${TMP_ROOT}/fix-hook.yaml"
awk '/^kind: Job$/{found=1} found{print} found && /^---$/{exit}' "${TAG_RENDER}" >"${FIX_HOOK}"
grep -Fq 'kind: Job' "${FIX_HOOK}"
grep -Fq 'name: stratum-fix-provider-keys' "${FIX_HOOK}"
grep -Fq 'helm.sh/hook: pre-upgrade' "${FIX_HOOK}"
grep -Fq 'image: "registry.cn-hangzhou.aliyuncs.com/stratum-demo/stratum-backend:demo"' "${FIX_HOOK}"
if grep -Eq 'image:[[:space:]]*"?<no value>|image:[[:space:]]*"?<nil>' "${FIX_HOOK}"; then
    echo 'fix-provider-keys hook image is not rendered (template bug)' >&2
    exit 1
fi
grep -Fq 'key: POSTGRES_PASSWORD' "${FIX_HOOK}"
grep -Fq 'key: JWT_PRIVATE_KEY_PEM' "${FIX_HOOK}"
grep -Fq 'key: DATA_ENCRYPTION_KEY' "${FIX_HOOK}"
grep -Fq 'name: "stratum-secrets"' "${FIX_HOOK}"
grep -Fq 'command:' "${FIX_HOOK}"
grep -Fq './fix-provider-keys' "${FIX_HOOK}"

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

# hook Job 必须与 deployment 共用 app digest(同一 stratum.image helper)。
FIX_HOOK_DIGEST="${TMP_ROOT}/fix-hook-digest.yaml"
awk '/^kind: Job$/{found=1} found{print} found && /^---$/{exit}' "${DIGEST_RENDER}" >"${FIX_HOOK_DIGEST}"
app_digest="sha256:$(printf '%064x' 1)"
grep -Fq "registry.cn-hangzhou.aliyuncs.com/stratum-demo/stratum-backend@${app_digest}" \
    "${FIX_HOOK_DIGEST}"

helm template stratum "${ROOT}/helm" \
    -f "${ROOT}/helm/values-demo.yaml" \
    -f "${REMOTE_HTTP_VALUES}" \
    --set-string config.frontendUrl=https://203.0.113.10:8443 \
    --set-string config.githubCallbackUrl=https://203.0.113.10:8443/api/auth/github/callback \
    >"${REMOTE_HTTP_RENDER}"

grep -Fq 'FRONTEND_URL: "https://203.0.113.10:8443"' "${REMOTE_HTTP_RENDER}"
grep -Fq 'GITHUB_CALLBACK_URL: "https://203.0.113.10:8443/api/auth/github/callback"' "${REMOTE_HTTP_RENDER}"
grep -Fq 'SECURE_COOKIES: "true"' "${REMOTE_HTTP_RENDER}"
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
grep -Eq 'traefik\.ingress\.kubernetes\.io/router\.entrypoints:[[:space:]]*"?websecure"?$' \
    "${REMOTE_HTTP_INGRESS}"
grep -Eq 'router\.tls:[[:space:]]*"?true"?$' "${REMOTE_HTTP_INGRESS}"
grep -Eq '[[:space:]]+tls:' "${REMOTE_HTTP_INGRESS}" || {
    echo 'remote HTTPS Ingress is missing TLS section' >&2
    exit 1
}

echo 'Helm image rendering tests passed'
