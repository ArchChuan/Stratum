#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "${TMP_ROOT}"' EXIT

TAG_RENDER="${TMP_ROOT}/tag.yaml"
DIGEST_RENDER="${TMP_ROOT}/digest.yaml"

helm template stratum "${ROOT}/helm" -f "${ROOT}/helm/values-demo.yaml" >"${TAG_RENDER}"
grep -Fq 'registry.cn-hangzhou.aliyuncs.com/stratum-demo/stratum-backend:demo' "${TAG_RENDER}"

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

echo 'Helm image rendering tests passed'
