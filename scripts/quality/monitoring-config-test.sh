#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC1091
source "${ROOT}/monitoring/remote/versions.env"

umask 077
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "${TMP_ROOT}"' EXIT
KPS_RENDER="${TMP_ROOT}/kube-prometheus-stack.yaml"
RENDERED_ALERTMANAGER="${TMP_ROOT}/alertmanager.yaml"
ALERTMANAGER_B64="${TMP_ROOT}/alertmanager.b64"

helm template kps prometheus-community/kube-prometheus-stack \
    --version "${KUBE_PROMETHEUS_STACK_CHART_VERSION}" \
    --namespace "${MONITORING_NAMESPACE}" \
    -f "${ROOT}/monitoring/remote/kube-prometheus-stack-values.yaml" >"${KPS_RENDER}"

go run "${ROOT}/scripts/quality/alertmanager-routing-test.go" extract-secret "${KPS_RENDER}" \
    alertmanager-kps-kube-prometheus-stack-alertmanager >"${ALERTMANAGER_B64}"
base64 -d "${ALERTMANAGER_B64}" >"${RENDERED_ALERTMANAGER}"
go run "${ROOT}/scripts/quality/alertmanager-routing-test.go" compare \
    "${ROOT}/monitoring/remote/alertmanager/alertmanager.yaml" "${RENDERED_ALERTMANAGER}"

BLACKBOX_RENDER="${TMP_ROOT}/blackbox.yaml"
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
amtool check-config "${RENDERED_ALERTMANAGER}"
bash "${ALERTMANAGER_ROUTING_TEST:-${ROOT}/scripts/quality/alertmanager-routing-test.sh}" \
    "${RENDERED_ALERTMANAGER}" "${ROOT}/monitoring/remote/alertmanager/alertmanager-test.yaml"
bash "${MONITORING_DASHBOARD_TEST:-${ROOT}/scripts/quality/monitoring-dashboard-test.sh}"

if [[ -d "${ROOT}/monitoring/remote/dashboards" ]]; then
    find "${ROOT}/monitoring/remote/dashboards" -type f -name '*.json' -print0 | \
        xargs -0 -r -n1 jq -e . >/dev/null
fi
