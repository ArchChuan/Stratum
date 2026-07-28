#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
RUNBOOK_ROOT="${RUNBOOK_ROOT:-${ROOT}}"
# shellcheck disable=SC1091
source "${ROOT}/monitoring/remote/versions.env"

umask 077
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "${TMP_ROOT}"' EXIT
KPS_RENDER="${TMP_ROOT}/kube-prometheus-stack.yaml"
RENDERED_ALERTMANAGER="${TMP_ROOT}/alertmanager.yaml"
ALERTMANAGER_B64="${TMP_ROOT}/alertmanager.b64"
RUNBOOK_INDEX="${TMP_ROOT}/runbooks.tsv"

find "${ROOT}/monitoring/remote/rules" -type f -name '*.yaml' -print0 | \
    xargs -0 awk '
        /^[[:space:]]*-[[:space:]]+alert:/ {
            alert_name = $0
            sub(/^[[:space:]]*-[[:space:]]+alert:[[:space:]]*/, "", alert_name)
            gsub(/"/, "", alert_name)
            gsub(/\047/, "", alert_name)
        }
        /^[[:space:]]*runbook_url:/ {
            runbook_url = $0
            sub(/^[[:space:]]*runbook_url:[[:space:]]*/, "", runbook_url)
            gsub(/"/, "", runbook_url)
            gsub(/\047/, "", runbook_url)
            print alert_name "\t" runbook_url
        }
    ' >"${RUNBOOK_INDEX}"

while IFS=$'\t' read -r alert_name runbook_url; do
    if [[ -z "${alert_name}" || "${runbook_url}" != /docs/*#* ]]; then
        echo "invalid runbook_url mapping: alert=${alert_name:-<missing>} url=${runbook_url:-<missing>}" >&2
        exit 1
    fi
    runbook_path="${runbook_url%%#*}"
    runbook_anchor="${runbook_url#*#}"
    repository_file="${RUNBOOK_ROOT}${runbook_path}"
    if [[ ! -f "${repository_file}" ]]; then
        echo "runbook file not found for ${alert_name}: ${runbook_path}" >&2
        exit 1
    fi
    if ! awk -v anchor="${runbook_anchor}" -v alert="${alert_name}" '
        $0 == "<a id=\"" anchor "\"></a>" { anchor_seen = 1; next }
        anchor_seen && $0 == "## " alert { found = 1; exit }
        anchor_seen && $0 != "" { exit }
        END { exit(found ? 0 : 1) }
    ' "${repository_file}"; then
        echo "runbook heading not found for ${alert_name}: ${runbook_path}#${runbook_anchor}" >&2
        exit 1
    fi
done <"${RUNBOOK_INDEX}"

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
