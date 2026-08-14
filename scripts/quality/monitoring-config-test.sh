#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
RUNBOOK_ROOT="${RUNBOOK_ROOT:-${ROOT}}"
MONITORING_RULES_DIR="${MONITORING_RULES_DIR:-${ROOT}/monitoring/remote/rules}"
# shellcheck disable=SC1091
source "${ROOT}/monitoring/remote/versions.env"

umask 077
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "${TMP_ROOT}"' EXIT
KPS_RENDER="${TMP_ROOT}/kube-prometheus-stack.yaml"
RENDERED_ALERTMANAGER="${TMP_ROOT}/alertmanager.yaml"
ALERTMANAGER_B64="${TMP_ROOT}/alertmanager.b64"
go run "${ROOT}/scripts/quality/monitoring-runbook-test.go" \
    "${MONITORING_RULES_DIR}" "${RUNBOOK_ROOT}"

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

# 规则双栈渲染器：远端 CRD 与本地 standalone 均须与 commit 产物一致，防止漂移。
# 单一规则源 monitoring/remote/rules，deploy 脚本与本地 prometheus 共用。
bash "${ROOT}/scripts/quality/render-monitoring-rules.sh" remote-test --check
bash "${ROOT}/scripts/quality/render-monitoring-rules.sh" local --check

# 本地 standalone 规则语法 + 本地 prometheus.yml 配置合法性。
# rule_files 指向容器内挂载路径，check 前替换为仓库内真实路径以解析 relabel 与规则文件。
promtool check rules "${ROOT}"/monitoring/local/rules/*.yml
LOCAL_PROM_CHECK="${TMP_ROOT}/prometheus-local-check.yml"
sed 's#- "/etc/prometheus/rules/\*\.yml"#- "'"${ROOT}"'/monitoring/local/rules/*.yml"#' \
    "${ROOT}/prometheus.yml" >"${LOCAL_PROM_CHECK}"
promtool check config "${LOCAL_PROM_CHECK}"

# Collector 三份配置（本地 docker / k8s tracing / k8s opik）的 tail_sampling
# 基础采样策略必须语义一致，防止演进漂移。chat-always 为 k8s 特有，不属共享集合。
go run "${ROOT}/scripts/quality/collector-tail-sampling-test.go" \
    "${ROOT}/otel-collector-config.yaml" \
    "${ROOT}/k8s/tracing.yaml" \
    "${ROOT}/k8s/opik-otel-collector.yaml"
