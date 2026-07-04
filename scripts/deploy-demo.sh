#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RELEASE_NAME="${RELEASE_NAME:-stratum}"
NAMESPACE="${NAMESPACE:-stratum}"
VALUES_FILE="${VALUES_FILE:-${ROOT_DIR}/helm/values-demo.yaml}"
if [[ "${RELEASE_NAME}" == *stratum* ]]; then
  FULLNAME="${RELEASE_NAME}"
else
  FULLNAME="${RELEASE_NAME}-stratum"
fi

cd "${ROOT_DIR}"

helm lint ./helm
helm template "${RELEASE_NAME}" ./helm -f "${VALUES_FILE}" >/tmp/stratum-demo-rendered.yaml

kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

helm upgrade --install "${RELEASE_NAME}" ./helm \
  --namespace "${NAMESPACE}" \
  -f "${VALUES_FILE}" \
  --wait \
  --timeout 10m

for deployment in \
  "${FULLNAME}-postgresql" \
  "${FULLNAME}-redis" \
  "${FULLNAME}-nats" \
  "${FULLNAME}-etcd" \
  "${FULLNAME}-minio" \
  "${FULLNAME}-milvus" \
  "${FULLNAME}" \
  "${FULLNAME}-frontend"; do
  if kubectl get deployment "${deployment}" -n "${NAMESPACE}" >/dev/null 2>&1; then
    kubectl rollout status deployment/"${deployment}" -n "${NAMESPACE}" --timeout=180s
  fi
done
