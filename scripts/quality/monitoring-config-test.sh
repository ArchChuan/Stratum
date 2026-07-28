#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC1091
source "${ROOT}/monitoring/remote/versions.env"

helm template kps prometheus-community/kube-prometheus-stack \
    --version "${KUBE_PROMETHEUS_STACK_CHART_VERSION}" \
    --namespace "${MONITORING_NAMESPACE}" \
    -f "${ROOT}/monitoring/remote/kube-prometheus-stack-values.yaml" >/dev/null
BLACKBOX_RENDER="$(mktemp)"
trap 'rm -f "${BLACKBOX_RENDER}"' EXIT
helm template stratum-blackbox prometheus-community/prometheus-blackbox-exporter \
    --version "${BLACKBOX_EXPORTER_CHART_VERSION}" \
    --namespace "${MONITORING_NAMESPACE}" \
    -f "${ROOT}/monitoring/remote/blackbox-exporter-values.yaml" >"${BLACKBOX_RENDER}"
grep -Eq 'replacement:[[:space:]]*"?remote-test"?$' "${BLACKBOX_RENDER}"
grep -Eq 'targetLabel:[[:space:]]*"?environment"?$' "${BLACKBOX_RENDER}"
grep -Eq 'replacement:[[:space:]]*"?stratum"?$' "${BLACKBOX_RENDER}"
grep -Eq 'targetLabel:[[:space:]]*"?service"?$' "${BLACKBOX_RENDER}"

if [[ -d "${ROOT}/monitoring/remote/rules" ]]; then
    find "${ROOT}/monitoring/remote/rules" -type f -name '*.yaml' -print0 | \
        xargs -0 -r promtool check rules
fi
promtool test rules "${ROOT}/monitoring/remote/tests/stratum-rules.test.yaml"
amtool check-config "${ROOT}/monitoring/remote/alertmanager/alertmanager.yaml"

if [[ -d "${ROOT}/monitoring/remote/dashboards" ]]; then
    find "${ROOT}/monitoring/remote/dashboards" -type f -name '*.json' -print0 | \
        xargs -0 -r -n1 jq -e . >/dev/null
fi
