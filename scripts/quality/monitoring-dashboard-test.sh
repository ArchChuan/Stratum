#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DASHBOARD_DIR="${ROOT}/monitoring/remote/dashboards"
MANIFEST="${ROOT}/monitoring/remote/resources/dashboards.yaml"
RUNBOOK_PATH="/docs/operations/remote-monitoring-runbook.md"

dashboard_names=(
    stratum-service-overview
    stratum-http
    stratum-resources
    stratum-dependencies
)
dashboard_uids=(
    stratum-remote-overview
    stratum-remote-http
    stratum-remote-resources
    stratum-remote-dependencies
)

fail() {
    printf 'monitoring dashboard contract failed: %s\n' "$*" >&2
    exit 1
}

generate_manifest() {
    local output="$1"
    local index file

    printf 'apiVersion: v1\nkind: List\nitems:\n' >"${output}"
    for index in "${!dashboard_names[@]}"; do
        file="${DASHBOARD_DIR}/${dashboard_names[index]}.json"
        [[ -f "${file}" ]] || fail "missing ${file#"${ROOT}/"}"
        printf '%s\n' \
            '  - apiVersion: v1' \
            '    kind: ConfigMap' \
            '    metadata:' \
            "      name: ${dashboard_names[index]}" \
            '      namespace: monitoring' \
            '      labels:' \
            '        grafana_dashboard: "1"' \
            '        app.kubernetes.io/part-of: stratum-monitoring' \
            '    data:' \
            "      ${dashboard_names[index]}.json: |-" >>"${output}"
        jq -S . "${file}" | sed 's/^/        /' >>"${output}"
    done
}

if [[ "${1:-}" == "--generate" ]]; then
    mkdir -p "$(dirname "${MANIFEST}")"
    generate_manifest "${MANIFEST}"
    exit 0
fi

[[ -d "${DASHBOARD_DIR}" ]] || fail "missing monitoring/remote/dashboards"

mapfile -t actual_files < <(find "${DASHBOARD_DIR}" -maxdepth 1 -type f -name '*.json' -printf '%f\n' | sort)
[[ "${#actual_files[@]}" -eq "${#dashboard_names[@]}" ]] || fail 'expected exactly four dashboard JSON files'

TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "${TMP_ROOT}"' EXIT
PROM_RULES="${TMP_ROOT}/dashboard-queries.yaml"
printf 'groups:\n  - name: stratum-dashboard-query-contract\n    rules:\n' >"${PROM_RULES}"

for index in "${!dashboard_names[@]}"; do
    file="${DASHBOARD_DIR}/${dashboard_names[index]}.json"
    [[ -f "${file}" ]] || fail "missing ${file#"${ROOT}/"}"
    jq -e . "${file}" >/dev/null || fail "invalid JSON in ${file#"${ROOT}/"}"
    jq -e --arg uid "${dashboard_uids[index]}" --arg runbook "${RUNBOOK_PATH}" '
        .uid == $uid and
        (.title | type == "string" and length > 0) and
        (.tags | index("stratum") != null and index("remote-test") != null) and
        (.links | any(.url | contains($runbook))) and
        (.schemaVersion | type == "number") and
        (.panels | type == "array" and length > 0)
    ' "${file}" >/dev/null || fail "metadata contract failed for ${file#"${ROOT}/"}"

    jq -e '
        [.. | objects | .datasource? | select(. != null) |
            if type == "object" then .uid else . end] |
        length > 0 and all(. == "prometheus")
    ' "${file}" >/dev/null || fail "non-prometheus or missing datasource in ${file#"${ROOT}/"}"

    jq -e '
        [.panels[] | select(.type != "row") | .gridPos] as $panels |
        ($panels | length > 0 and all(
            (.x | type == "number") and (.y | type == "number") and
            (.w | type == "number") and (.h | type == "number") and
            .x >= 0 and .y >= 0 and .w > 0 and .h > 0 and (.x + .w) <= 24
        )) and
        all(range(0; $panels | length); . as $left |
            all(range($left + 1; $panels | length); . as $right |
                (($panels[$left].x + $panels[$left].w <= $panels[$right].x) or
                 ($panels[$right].x + $panels[$right].w <= $panels[$left].x) or
                 ($panels[$left].y + $panels[$left].h <= $panels[$right].y) or
                 ($panels[$right].y + $panels[$right].h <= $panels[$left].y))))
    ' "${file}" >/dev/null || fail "unstable or out-of-grid panel dimensions in ${file#"${ROOT}/"}"

    if jq -e '[.templating.list[]? | ((.name // "") + " " + (.query // ""))] | join(" ") |
        test("(tenant|user)(_|-)?id"; "i")' "${file}" >/dev/null; then
        fail "tenant/user identifier variable in ${file#"${ROOT}/"}"
    fi
    if jq -e '[.. | objects | .expr? | select(type == "string")] | join(" ") |
        test("(tenant|user)(_|-)?id|vector\\s*\\(\\s*0\\s*\\)|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"; "i")' "${file}" >/dev/null; then
        fail "sensitive identifier or zero-filled missing data in ${file#"${ROOT}/"}"
    fi

    while IFS= read -r expression; do
        printf '      - record: dashboard_contract_query_%d\n        expr: %s\n' \
            "$(wc -l <"${PROM_RULES}")" "$(jq -Rn --arg value "${expression}" '$value')" >>"${PROM_RULES}"
    done < <(jq -r '.. | objects | .expr? | select(type == "string" and length > 0)' "${file}")
done

jq -e '[.. | objects | .expr? | select(type == "string")] | join(" ") |
    test("probe_success") and test("stratum:http_requests:increase5m") and
    test("stratum:http_5xx_ratio:ratio5m") and
    test("stratum:http_request_duration_seconds:p95_5m") and
    test("kube_deployment_status_replicas_ready") and test("ALERTS")' \
    "${DASHBOARD_DIR}/stratum-service-overview.json" >/dev/null || fail 'overview core queries are incomplete'

jq -e '[.. | objects | .expr? | select(type == "string")] | join(" ") |
    test("stratum:http_requests_by_path:increase5m") and
    test("stratum:http_5xx_ratio_by_path:ratio5m") and
    test("stratum:http_request_duration_seconds_by_path:p95_5m") and
    (test("http_requests_total|http_request_duration_seconds_bucket") | not)' \
    "${DASHBOARD_DIR}/stratum-http.json" >/dev/null || fail 'HTTP dashboard must use bounded recording rules'
jq -e '[.panels[].targets[]? | select(.expr | contains("_by_path:")) | .legendFormat] |
    length == 3 and all(. == "{{path}}")' \
    "${DASHBOARD_DIR}/stratum-http.json" >/dev/null || fail 'HTTP dashboard must identify bounded path series'

jq -e '[.. | objects | .expr? | select(type == "string")] | join(" ") |
    test("stratum:kube_pod:placement") and test("node_cpu_seconds_total") and
    test("node_memory_MemAvailable_bytes") and test("container_memory_working_set_bytes") and
    test("container_cpu_cfs_throttled_periods_total") and test("kubelet_volume_stats_used_bytes")' \
    "${DASHBOARD_DIR}/stratum-resources.json" >/dev/null || fail 'resource dashboard does not match Task 7 metrics'

jq -e '[.. | objects | .expr? | select(type == "string")] as $queries |
    ($queries | join(" ") | test("stratum-etcd-metrics") and test("stratum-milvus-metrics") and
        test("stratum-feishu-alert-adapter") and test("kps-kube-prometheus-stack-prometheus") and
        test("kps-kube-prometheus-stack-alertmanager") and test("stratum-blackbox-prometheus-blackbox-exporter")) and
    ($queries | all(test("^up\\{"))) and
    ([.panels[] | select(.type == "text") | .options.content] | join(" ") | test("不支持|未覆盖"))' \
    "${DASHBOARD_DIR}/stratum-dependencies.json" >/dev/null || fail 'dependency dashboard exceeds verified target coverage'

read -r -a PROMTOOL_COMMAND <<<"${PROMTOOL_COMMAND:-promtool}"
command -v "${PROMTOOL_COMMAND[0]}" >/dev/null || fail 'promtool is required to parse dashboard queries'
"${PROMTOOL_COMMAND[@]}" check rules "${PROM_RULES}" >/dev/null

[[ -f "${MANIFEST}" ]] || fail "missing ${MANIFEST#"${ROOT}/"}"
GENERATED="${TMP_ROOT}/dashboards.yaml"
generate_manifest "${GENERATED}"
cmp -s "${GENERATED}" "${MANIFEST}" || fail 'dashboard ConfigMaps are stale; run monitoring-dashboard-test.sh --generate'
go run "${ROOT}/scripts/quality/monitoring-dashboard-roundtrip.go" "${MANIFEST}" "${DASHBOARD_DIR}"

printf 'Monitoring dashboard contracts passed\n'
