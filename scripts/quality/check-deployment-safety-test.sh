#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORKFLOW="${ROOT}/.github/workflows/deploy.yml"
CI_WORKFLOW="${ROOT}/.github/workflows/ci.yml"

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

# ── 生产并发保护 ──
require 'group:[[:space:]]*stratum-production' 'fixed production concurrency group'
require 'cancel-in-progress:[[:space:]]*false' 'non-cancelling active deployment'

# ── 测试门禁：workflow_run CI 或 job 级 needs:test ──
if grep -Eq 'workflow_run:' "${WORKFLOW}" && grep -Eq 'workflows:[[:space:]]*\[CI\]' "${WORKFLOW}"; then
    require 'github\.event\.workflow_run\.conclusion.*success' 'workflow_run CI success gate'
elif ! grep -Eq 'needs:[[:space:]]*test' "${WORKFLOW}"; then
    echo 'deployment safety contract missing: test dependency (workflow_run CI or needs:test)' >&2
    exit 1
fi

# ── 镜像完整性：SHA256 digest 固定 ──
require 'sha256:\[0-9a-f\]\{64\}' 'registry digest validation'
require 'jq -e .all.*images.*test.*@sha256' 'deployment receipt image digest check'
reject 'minio/minio:latest|/minio:latest' 'mutable MinIO latest tag'
reject 'metrics-server/releases/latest' 'mutable metrics-server latest manifest'

# ── 部署原子性与失败传播 ──
reject 'continue-on-error:[[:space:]]*true' 'deployment errors are not suppressed'
reject '\|\|[[:space:]]*true' 'suppressed deployment errors'

# ── SSH / TLS / 证书安全 ──
reject 'StrictHostKeyChecking=no' 'disabled SSH host verification'
reject 'insecure-skip-tls-verify|certificate-authority-data:/d' 'disabled Kubernetes API verification'
require_file "${ROOT}/helm/values-demo-remote-http.yaml" 'secureCookies:[[:space:]]*"true"' 'HTTPS secure cookies'
require_file "${ROOT}/helm/templates/deployment.yaml" 'checksum/secret:' 'Secret rollout checksum'

# ── 安全硬化（Feishu adapter） ──
for hardening in \
    'readOnlyRootFilesystem:[[:space:]]*true' \
    'automountServiceAccountToken:[[:space:]]*false' \
    'runAsNonRoot:[[:space:]]*true' \
    'allowPrivilegeEscalation:[[:space:]]*false' \
    'type:[[:space:]]*RuntimeDefault'; do
    require_file "${ROOT}/monitoring/remote/resources/feishu-alert-adapter.yaml" \
        "${hardening}" "adapter hardening: ${hardening}"
done

# ── 禁止凭证打印 ──
reject_file "${ROOT}/scripts/e2e/platform-assistant-remote-verify.sh" \
    'echo.*(token|password|api[_-]?key)' 'remote verifier prints a credential'

# ── 禁止破坏性操作 ──
reject_file "${ROOT}/scripts/deploy-remote-monitoring.sh" \
    'helm uninstall|kubectl delete .*(customresourcedefinition|crd|persistentvolumeclaim|pvc)|k8s/monitoring\.yaml' \
    'monitoring deployment contains destructive or stale operations'

echo 'deployment safety contract tests passed'
