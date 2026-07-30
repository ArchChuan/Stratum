#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC1091
source "${ROOT}/monitoring/remote/versions.env"

umask 077
INVENTORY_DIR="$(mktemp -d)"
PORT_FORWARD_PID=""

cleanup() {
    if [[ -n "${PORT_FORWARD_PID}" ]] && kill -0 "${PORT_FORWARD_PID}" >/dev/null 2>&1; then
        kill "${PORT_FORWARD_PID}" >/dev/null 2>&1 || :
        wait "${PORT_FORWARD_PID}" >/dev/null 2>&1 || :
    fi
    /usr/bin/rm -rf -- "${INVENTORY_DIR}"
    if [[ "${MONITORING_DEPLOY_TEST_REPORT_CLEANUP:-}" == "1" ]]; then
        echo "inventory directory removed: ${INVENTORY_DIR}" >&2
    fi
}

exit_for_signal() {
    local status="$1"
    trap - INT TERM
    exit "${status}"
}

trap cleanup EXIT
trap 'exit_for_signal 130' INT
trap 'exit_for_signal 143' TERM

require_command() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "required command is unavailable: $1" >&2
        exit 1
    }
}

for command_name in helm kubectl curl jq sed; do
    require_command "${command_name}"
done

inventory_cluster() {
    local releases="${INVENTORY_DIR}/helm-releases.json"
    helm list --all-namespaces --output json >"${releases}"

    if ! jq -e --arg namespace "${MONITORING_NAMESPACE}" \
        --arg kps_release "${KUBE_PROMETHEUS_STACK_RELEASE}" \
        --arg kps_chart "kube-prometheus-stack-${KUBE_PROMETHEUS_STACK_CHART_VERSION}" \
        --arg blackbox_release "${BLACKBOX_EXPORTER_RELEASE}" \
        --arg blackbox_chart "prometheus-blackbox-exporter-${BLACKBOX_EXPORTER_CHART_VERSION}" '
          [ .[] | select(
              .namespace == $namespace or
              .name == $kps_release or
              .name == $blackbox_release or
              (.chart | startswith("kube-prometheus-stack-")) or
              (.chart | startswith("prometheus-blackbox-exporter-"))
            ) ] |
          all(.[];
            (.name == $kps_release and .namespace == $namespace and .chart == $kps_chart) or
            (.name == $blackbox_release and .namespace == $namespace and .chart == $blackbox_chart)
          )' "${releases}" >/dev/null; then
        echo "monitoring release inventory does not match the pinned release, namespace, and chart contract" >&2
        exit 1
    fi

    local release
    for release in "${KUBE_PROMETHEUS_STACK_RELEASE}" "${BLACKBOX_EXPORTER_RELEASE}"; do
        if jq -e --arg release "${release}" --arg namespace "${MONITORING_NAMESPACE}" \
            '.[] | select(.name == $release and .namespace == $namespace)' "${releases}" >/dev/null; then
            helm status "${release}" --namespace "${MONITORING_NAMESPACE}" \
                --output json >"${INVENTORY_DIR}/helm-${release}-status.json"
            helm get values "${release}" --namespace "${MONITORING_NAMESPACE}" \
                --all --output yaml >"${INVENTORY_DIR}/helm-${release}-values.yaml"
        fi
    done

    local cr_types="${INVENTORY_DIR}/monitoring-custom-resource-types.txt"
    local cr_names="${INVENTORY_DIR}/monitoring-custom-resources.txt"
    kubectl api-resources --api-group=monitoring.coreos.com --namespaced=true \
        --output name >"${cr_types}"
    : >"${cr_names}"
    while IFS= read -r resource_type; do
        [[ -n "${resource_type}" ]] || continue
        kubectl get "${resource_type}" --all-namespaces --output name >>"${cr_names}"
    done <"${cr_types}"
    kubectl get pvc --all-namespaces --output name >"${INVENTORY_DIR}/persistent-volume-claims.txt"
    kubectl get configmap --all-namespaces -l grafana_dashboard=1 \
        --output name >"${INVENTORY_DIR}/grafana-dashboard-configmaps.txt"
    kubectl get configmap --all-namespaces -l grafana_datasource=1 \
        --output name >"${INVENTORY_DIR}/grafana-datasource-configmaps.txt"
}

validate_repository() {
    if [[ -n "${MONITORING_VALIDATION_COMMAND:-}" ]]; then
        bash -c "${MONITORING_VALIDATION_COMMAND}"
        return
    fi
    bash "${ROOT}/scripts/quality/monitoring-config-test.sh"
}

render_prometheus_rules() {
    local output="${INVENTORY_DIR}/stratum-prometheus-rules.yaml"
    {
        printf '%s\n' 'apiVersion: monitoring.coreos.com/v1' 'kind: PrometheusRule' 'metadata:' \
            '  name: stratum-remote-rules' '  namespace: monitoring' '  labels:' '    release: kps' \
            'spec:' '  groups:'
        for rule_file in "${ROOT}"/monitoring/remote/rules/*.yaml; do
            tail -n +2 "${rule_file}" | sed 's/^/  /'
        done
    } >"${output}"
    printf '%s\n' "${output}"
}

reconcile_monitoring() {
    : "${FEISHU_ADAPTER_IMAGE:?FEISHU_ADAPTER_IMAGE must be an immutable registry image digest}"
    if [[ ! "${FEISHU_ADAPTER_IMAGE}" =~ @sha256:[0-9a-f]{64}$ ]]; then
        echo "FEISHU_ADAPTER_IMAGE must end with an immutable sha256 digest" >&2
        exit 1
    fi

    helm upgrade --install "${KUBE_PROMETHEUS_STACK_RELEASE}" \
        prometheus-community/kube-prometheus-stack \
        --version "${KUBE_PROMETHEUS_STACK_CHART_VERSION}" \
        --namespace "${MONITORING_NAMESPACE}" --create-namespace \
        -f "${ROOT}/monitoring/remote/kube-prometheus-stack-values.yaml" \
        --atomic --wait --timeout 15m

    helm upgrade --install "${BLACKBOX_EXPORTER_RELEASE}" \
        prometheus-community/prometheus-blackbox-exporter \
        --version "${BLACKBOX_EXPORTER_CHART_VERSION}" \
        --namespace "${MONITORING_NAMESPACE}" \
        -f "${ROOT}/monitoring/remote/blackbox-exporter-values.yaml" \
        --atomic --wait --timeout 10m

    kubectl wait --for=condition=Established crd/servicemonitors.monitoring.coreos.com --timeout=2m
    kubectl wait --for=condition=Established crd/prometheusrules.monitoring.coreos.com --timeout=2m

    local adapter_manifest="${INVENTORY_DIR}/feishu-alert-adapter.yaml"
    sed "s|__FEISHU_ADAPTER_IMAGE__|${FEISHU_ADAPTER_IMAGE}|" \
        "${ROOT}/monitoring/remote/resources/feishu-alert-adapter.yaml" >"${adapter_manifest}"
    kubectl apply -f "${adapter_manifest}"
    kubectl apply -f "${ROOT}/monitoring/remote/resources/dependency-monitors.yaml"
    kubectl apply -f "${ROOT}/monitoring/remote/resources/stratum-backend-monitor.yaml"
    kubectl apply -f "${ROOT}/monitoring/remote/resources/dashboards.yaml"
    kubectl apply -f "$(render_prometheus_rules)"

    kubectl rollout restart deployment/stratum-feishu-alert-adapter -n "${MONITORING_NAMESPACE}"
    kubectl rollout status deployment/stratum-feishu-alert-adapter \
        -n "${MONITORING_NAMESPACE}" --timeout=5m
    kubectl rollout status deployment/kps-kube-prometheus-stack-operator \
        -n "${MONITORING_NAMESPACE}" --timeout=5m
}

wait_for_prometheus() {
    local attempt
    for attempt in $(seq 1 30); do
        if curl --fail --silent --show-error --max-time 2 http://127.0.0.1:19090/-/ready >/dev/null; then
            return
        fi
        if ! kill -0 "${PORT_FORWARD_PID}" >/dev/null 2>&1; then
            echo "Prometheus port-forward stopped unexpectedly" >&2
            exit 1
        fi
        sleep 1
    done
    echo "Prometheus did not become ready through the temporary port-forward" >&2
    exit 1
}

verify_prometheus() {
    kubectl port-forward --namespace "${MONITORING_NAMESPACE}" \
        service/kps-kube-prometheus-stack-prometheus 19090:9090 \
        >"${INVENTORY_DIR}/prometheus-port-forward.log" 2>&1 &
    PORT_FORWARD_PID=$!
    wait_for_prometheus

    local targets="${INVENTORY_DIR}/prometheus-targets.json"
    local probe="${INVENTORY_DIR}/prometheus-public-probe.json"
    local rules="${INVENTORY_DIR}/prometheus-rules.json"
    local smoke_attempts="${MONITORING_SMOKE_ATTEMPTS:-30}"
    local smoke_interval="${MONITORING_SMOKE_INTERVAL_SEC:-2}"
    local targets_healthy=false
    local probe_healthy=false
    local rules_healthy=false
    local attempt

    for attempt in $(seq 1 "${smoke_attempts}"); do
        if curl --fail --silent --show-error --max-time 10 \
            http://127.0.0.1:19090/api/v1/targets >"${targets}" && jq -e '
      .status == "success" and
      ([.data.activeTargets[] | select(.health == "up") | .labels] as $targets |
        any($targets[]; .namespace == "stratum" and .service == "stratum" and .endpoint == "http") and
        any($targets[]; .namespace == "monitoring" and .service == "stratum-feishu-alert-adapter") and
        any($targets[]; .namespace == "stratum" and .service == "stratum-etcd-metrics") and
        any($targets[]; .namespace == "stratum" and .service == "stratum-milvus-metrics"))
        ' "${targets}" >/dev/null; then
            targets_healthy=true
            break
        fi
        sleep "${smoke_interval}"
    done
    if [[ "${targets_healthy}" != "true" ]]; then
        if [[ -s "${targets}" ]]; then
            jq -r '
          [.data.activeTargets[]] as $targets |
          [
            {name: "backend", present: any($targets[]; .labels.namespace == "stratum" and
                .labels.service == "stratum" and .labels.endpoint == "http"),
              up: any($targets[]; .health == "up" and .labels.namespace == "stratum" and
                .labels.service == "stratum" and .labels.endpoint == "http")},
            {name: "feishu-adapter", present: any($targets[]; .labels.namespace == "monitoring" and
                .labels.service == "stratum-feishu-alert-adapter"),
              up: any($targets[]; .health == "up" and .labels.namespace == "monitoring" and
                .labels.service == "stratum-feishu-alert-adapter")},
            {name: "etcd", present: any($targets[]; .labels.namespace == "stratum" and
                .labels.service == "stratum-etcd-metrics"),
              up: any($targets[]; .health == "up" and .labels.namespace == "stratum" and
                .labels.service == "stratum-etcd-metrics")},
            {name: "milvus", present: any($targets[]; .labels.namespace == "stratum" and
                .labels.service == "stratum-milvus-metrics"),
              up: any($targets[]; .health == "up" and .labels.namespace == "stratum" and
                .labels.service == "stratum-milvus-metrics")}
          ] | .[] | select(.up | not) |
          "monitoring target contract failed: \(.name) present=\(.present) up=\(.up)"
            ' "${targets}" >&2 || :
        fi
        echo "one or more expected monitoring targets are missing or unhealthy" >&2
        exit 1
    fi

    for attempt in $(seq 1 "${smoke_attempts}"); do
        if curl --fail --silent --show-error --max-time 10 --get \
            --data-urlencode 'query=probe_success{service="stratum",environment="remote-test",target="stratum-public-health"}' \
            http://127.0.0.1:19090/api/v1/query >"${probe}" && jq -e '
      .status == "success" and .data.resultType == "vector" and
      any(.data.result[];
        .metric.service == "stratum" and .metric.environment == "remote-test" and
        .metric.target == "stratum-public-health" and .value[1] == "1")
        ' "${probe}" >/dev/null; then
            probe_healthy=true
            break
        fi
        sleep "${smoke_interval}"
    done
    if [[ "${probe_healthy}" != "true" ]]; then
        if [[ -s "${probe}" ]]; then
            jq -r '
          [.data.result[]?] as $results |
          {present: any($results[];
              .metric.service == "stratum" and .metric.environment == "remote-test" and
              .metric.target == "stratum-public-health"),
            healthy: any($results[];
              .metric.service == "stratum" and .metric.environment == "remote-test" and
              .metric.target == "stratum-public-health" and .value[1] == "1")} |
          "monitoring probe contract failed: public-health present=\(.present) healthy=\(.healthy)"
            ' "${probe}" >&2 || :
        fi
        echo "public health probe sample is missing or unhealthy" >&2
        exit 1
    fi

    for attempt in $(seq 1 "${smoke_attempts}"); do
        if curl --fail --silent --show-error --max-time 10 \
            http://127.0.0.1:19090/api/v1/rules >"${rules}" && jq -e '
      .status == "success" and
      ([.data.groups[].name] as $groups |
        ["stratum-availability", "stratum-http-recording", "stratum-workloads", "stratum-capacity",
         "stratum-dependencies", "stratum-monitoring"] |
        all(.[]; . as $expected | $groups | index($expected) != null)) and
      all(.data.groups[]; (.lastError // "") == "")
        ' "${rules}" >/dev/null; then
            rules_healthy=true
            break
        fi
        sleep "${smoke_interval}"
    done
    if [[ "${rules_healthy}" != "true" ]]; then
        echo "expected Stratum rules are missing or Prometheus reports evaluation failures" >&2
        exit 1
    fi
}

inventory_cluster
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts --force-update
helm repo update prometheus-community
validate_repository
reconcile_monitoring
verify_prometheus
