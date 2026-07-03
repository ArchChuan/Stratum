#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RELEASE_NAME="${RELEASE_NAME:-stratum}"
NAMESPACE="${NAMESPACE:-stratum}"
VALUES_FILE="${VALUES_FILE:-${ROOT_DIR}/helm/values-demo.yaml}"

cd "${ROOT_DIR}"

helm lint ./helm
helm template "${RELEASE_NAME}" ./helm -f "${VALUES_FILE}" >/tmp/stratum-demo-rendered.yaml

kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

helm upgrade --install "${RELEASE_NAME}" ./helm \
  --namespace "${NAMESPACE}" \
  -f "${VALUES_FILE}" \
  --wait \
  --timeout 10m

kubectl rollout status deployment/"${RELEASE_NAME}" -n "${NAMESPACE}" --timeout=180s
kubectl rollout status deployment/"${RELEASE_NAME}"-frontend -n "${NAMESPACE}" --timeout=180s
