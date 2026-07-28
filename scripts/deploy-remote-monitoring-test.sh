#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEPLOY_SCRIPT="${ROOT}/scripts/deploy-remote-monitoring.sh"

if [[ ! -f "${DEPLOY_SCRIPT}" ]]; then
    echo "deploy script is missing: ${DEPLOY_SCRIPT}" >&2
    exit 1
fi

TEST_ROOT="$(mktemp -d)"
trap '/usr/bin/rm -rf -- "${TEST_ROOT}"' EXIT

fail() {
    echo "deploy monitoring test failed: $*" >&2
    exit 1
}

assert_contains() {
    local file="$1" pattern="$2" description="$3"
    grep -Eq -- "${pattern}" "${file}" || fail "${description}"
}

assert_not_contains() {
    local file="$1" pattern="$2" description="$3"
    if grep -Eq -- "${pattern}" "${file}"; then
        fail "${description}"
    fi
}

assert_inventory_cleaned() {
    local scenario="$1"
    local stderr_log="${TEST_ROOT}/${scenario}/stderr.log"
    assert_contains "${stderr_log}" '^inventory directory removed: ' \
        "${scenario} did not report private inventory cleanup"
    local inventory_dir
    inventory_dir=$(sed -n 's/^inventory directory removed: //p' "${stderr_log}" | tail -1)
    [[ -n "${inventory_dir}" && ! -e "${inventory_dir}" ]] || \
        fail "${scenario} left its private inventory directory behind"
}

write_fake_tools() {
    local bin_dir="$1"
    mkdir -p "${bin_dir}"

    cat >"${bin_dir}/helm" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf 'helm %s\n' "$*" >>"${CALL_LOG}"
case "${1:-}" in
    list)
        [[ "${SCENARIO:-}" != "inventory_failure" ]] || exit 41
        case "${SCENARIO:-}" in
            unexpected_release)
                printf '[{"name":"unexpected","namespace":"monitoring","chart":"other-1.0.0"}]\n'
                ;;
            unexpected_namespace)
                printf '[{"name":"kps","namespace":"other","chart":"kube-prometheus-stack-87.10.1"}]\n'
                ;;
            unexpected_chart)
                printf '[{"name":"kps","namespace":"monitoring","chart":"kube-prometheus-stack-86.0.0"}]\n'
                ;;
            *)
                printf '[{"name":"kps","namespace":"monitoring","chart":"kube-prometheus-stack-87.10.1"},{"name":"stratum-blackbox","namespace":"monitoring","chart":"prometheus-blackbox-exporter-11.15.1"}]\n'
                ;;
        esac
        ;;
    status)
        [[ "${SCENARIO:-}" != "status_failure" ]] || exit 47
        printf '{}\n'
        ;;
    get)
        [[ "${SCENARIO:-}" != "values_failure" ]] || exit 48
        printf '{}\n'
        ;;
    upgrade)
        [[ "${SCENARIO:-}" != "helm_failure" ]] || exit 42
        ;;
esac
FAKE

    cat >"${bin_dir}/kubectl" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf 'kubectl %s\n' "$*" >>"${CALL_LOG}"
case "$*" in
    *"api-resources --api-group=monitoring.coreos.com"*)
        printf '%s\n' prometheuses.monitoring.coreos.com alertmanagers.monitoring.coreos.com \
            servicemonitors.monitoring.coreos.com prometheusrules.monitoring.coreos.com probes.monitoring.coreos.com
        ;;
    *"get prometheuses.monitoring.coreos.com"*|*"get alertmanagers.monitoring.coreos.com"*|*"get servicemonitors.monitoring.coreos.com"*|*"get prometheusrules.monitoring.coreos.com"*|*"get probes.monitoring.coreos.com"*|*"get pvc"*|*"get configmap"*)
        printf 'name\n'
        ;;
    *"apply -f"*)
        [[ "${SCENARIO:-}" != "apply_failure" ]] || exit 43
        if [[ "$*" == *'stratum-prometheus-rules.yaml'* ]]; then
            rules_file="${*: -1}"
            grep -Eq '^kind: PrometheusRule$' "${rules_file}"
            for group in stratum-availability stratum-http-recording stratum-workloads stratum-capacity \
                stratum-dependencies stratum-monitoring; do
                grep -Eq "^[[:space:]]+- name: ${group}$" "${rules_file}"
            done
        fi
        ;;
    *"rollout status"*)
        [[ "${SCENARIO:-}" != "rollout_failure" ]] || exit 44
        ;;
    *"port-forward"*)
        printf 'port-forward-started %s\n' "$$" >>"${CALL_LOG}"
        : >"${PF_READY_FILE}"
        trap 'printf "port-forward-terminated %s\n" "$$" >>"${CALL_LOG}"; exit 0' TERM INT
        while :; do sleep 1; done
        ;;
esac
FAKE

    cat >"${bin_dir}/curl" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf 'curl %s\n' "$*" >>"${CALL_LOG}"
if [[ "$*" == *'/-/ready'* ]]; then
    for _ in $(seq 1 1000); do
        [[ -e "${PF_READY_FILE}" ]] && exit 0
    done
    exit 49
elif [[ "$*" == *'/api/v1/targets'* ]]; then
    [[ "${SCENARIO:-}" != "target_failure" ]] || exit 45
    printf '{"status":"success","data":{"activeTargets":[{"labels":{"namespace":"stratum","service":"stratum","endpoint":"http"},"health":"up"},{"labels":{"job":"stratum-blackbox-prometheus-blackbox-exporter","service":"stratum","environment":"remote-test"},"health":"up"},{"labels":{"namespace":"monitoring","service":"stratum-feishu-alert-adapter"},"health":"up"},{"labels":{"namespace":"stratum","service":"stratum-etcd-metrics"},"health":"up"},{"labels":{"namespace":"stratum","service":"stratum-milvus-metrics"},"health":"up"}]}}\n'
elif [[ "$*" == *'/api/v1/rules'* ]]; then
    if [[ "${SCENARIO:-}" == "rule_health_failure" ]]; then
        printf '{"status":"success","data":{"groups":[{"name":"stratum","evaluationTime":1,"lastError":"failed"}]}}\n'
        exit 0
    fi
    printf '{"status":"success","data":{"groups":[{"name":"stratum-availability","evaluationTime":1,"lastError":""},{"name":"stratum-http-recording","evaluationTime":1,"lastError":""},{"name":"stratum-workloads","evaluationTime":1,"lastError":""},{"name":"stratum-capacity","evaluationTime":1,"lastError":""},{"name":"stratum-dependencies","evaluationTime":1,"lastError":""},{"name":"stratum-monitoring","evaluationTime":1,"lastError":""}]}}\n'
else
    exit 46
fi
FAKE

    cat >"${bin_dir}/jq" <<'FAKE'
#!/usr/bin/env bash
exec /usr/bin/jq "$@"
FAKE

    chmod +x "${bin_dir}"/*
}

run_case() {
    local scenario="$1" expected="$2"
    local case_dir="${TEST_ROOT}/${scenario}"
    mkdir -p "${case_dir}"
    write_fake_tools "${case_dir}/bin"
    : >"${case_dir}/calls.log"

    set +e
    PATH="${case_dir}/bin:/usr/bin:/bin" CALL_LOG="${case_dir}/calls.log" SCENARIO="${scenario}" \
        PF_READY_FILE="${case_dir}/port-forward.ready" \
        FEISHU_ADAPTER_IMAGE='registry.example/adapter@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' \
        MONITORING_DEPLOY_TEST_REPORT_CLEANUP=1 \
        MONITORING_SMOKE_ATTEMPTS=1 MONITORING_SMOKE_INTERVAL_SEC=0 \
        MONITORING_VALIDATION_COMMAND="printf 'validation\\n' >>\"${case_dir}/calls.log\"" \
        bash "${DEPLOY_SCRIPT}" >"${case_dir}/stdout.log" 2>"${case_dir}/stderr.log"
    local status=$?
    set -e

    if [[ "${expected}" == success && ${status} -ne 0 ]]; then
        fail "${scenario} unexpectedly failed (status ${status}): $(<"${case_dir}/stderr.log")"
    fi
    if [[ "${expected}" == failure && ${status} -eq 0 ]]; then
        fail "${scenario} unexpectedly succeeded"
    fi

    assert_not_contains "${case_dir}/calls.log" \
        'helm uninstall|kubectl delete .*(customresourcedefinition|crd|persistentvolumeclaim|pvc)|k8s/monitoring\.yaml' \
        "${scenario} attempted a destructive or stale operation"
}

for scenario in inventory_failure status_failure values_failure unexpected_release unexpected_namespace unexpected_chart; do
    run_case "${scenario}" failure
    assert_not_contains "${TEST_ROOT}/${scenario}/calls.log" \
        'helm upgrade|kubectl apply|kubectl rollout|kubectl port-forward' \
        "${scenario} mutated the cluster after unsafe inventory"
done

for scenario in inventory_failure status_failure values_failure; do
    assert_inventory_cleaned "${scenario}"
done

for scenario in helm_failure apply_failure rollout_failure target_failure rule_health_failure; do
    run_case "${scenario}" failure
done
assert_contains "${TEST_ROOT}/helm_failure/calls.log" '^helm upgrade' \
    'Helm failure scenario did not reach the Helm mutation'
assert_contains "${TEST_ROOT}/apply_failure/calls.log" '^kubectl apply' \
    'apply failure scenario did not reach repository resource application'
assert_contains "${TEST_ROOT}/rollout_failure/calls.log" '^kubectl rollout status' \
    'rollout failure scenario did not reach the readiness gate'
assert_contains "${TEST_ROOT}/target_failure/calls.log" '/api/v1/targets' \
    'target failure scenario did not reach the target smoke check'
assert_contains "${TEST_ROOT}/rule_health_failure/calls.log" '/api/v1/rules' \
    'rule-health failure scenario did not reach the rule smoke check'
for scenario in target_failure rule_health_failure; do
    assert_inventory_cleaned "${scenario}"
    assert_contains "${TEST_ROOT}/${scenario}/calls.log" '^port-forward-terminated [0-9]+$' \
        "${scenario} left its Prometheus port-forward running"
    started_pid=$(sed -n 's/^port-forward-started //p' "${TEST_ROOT}/${scenario}/calls.log" | tail -1)
    terminated_pid=$(sed -n 's/^port-forward-terminated //p' "${TEST_ROOT}/${scenario}/calls.log" | tail -1)
    [[ -n "${started_pid}" && "${started_pid}" == "${terminated_pid}" ]] || \
        fail "${scenario} did not terminate the exact port-forward process it started"
    if kill -0 "${started_pid}" >/dev/null 2>&1; then
        fail "${scenario} returned while its Prometheus port-forward process was still alive"
    fi
done

run_case success success
SUCCESS_LOG="${TEST_ROOT}/success/calls.log"
assert_contains "${SUCCESS_LOG}" '^validation$' 'repository validation was not invoked'
assert_contains "${SUCCESS_LOG}" '^helm status kps --namespace monitoring --output json$' \
    'current kube-prometheus-stack status was not inventoried'
assert_contains "${SUCCESS_LOG}" '^helm get values stratum-blackbox --namespace monitoring --all --output yaml$' \
    'current blackbox values were not inventoried'
validation_line=$(grep -n '^validation$' "${SUCCESS_LOG}" | cut -d: -f1)
upgrade_line=$(grep -n '^helm upgrade' "${SUCCESS_LOG}" | head -1 | cut -d: -f1)
[[ ${validation_line} -lt ${upgrade_line} ]] || fail 'validation did not run before Helm mutation'

assert_contains "${SUCCESS_LOG}" \
    'helm upgrade --install kps prometheus-community/kube-prometheus-stack --version 87\.10\.1 --namespace monitoring --create-namespace .*--atomic --wait --timeout 15m' \
    'kube-prometheus-stack is not reconciled with the exact safe contract'
assert_contains "${SUCCESS_LOG}" \
    'helm upgrade --install stratum-blackbox prometheus-community/prometheus-blackbox-exporter --version 11\.15\.1 --namespace monitoring .*--atomic --wait --timeout 10m' \
    'blackbox exporter is not reconciled with the exact safe contract'
assert_contains "${SUCCESS_LOG}" 'kubectl rollout status deployment/stratum-feishu-alert-adapter -n monitoring' \
    'adapter rollout was not awaited'
assert_contains "${SUCCESS_LOG}" 'kubectl rollout status deployment/kps-kube-prometheus-stack-operator -n monitoring' \
    'operator rollout was not awaited'
assert_contains "${SUCCESS_LOG}" 'curl .*/api/v1/targets' 'Prometheus target smoke check missing'
assert_contains "${SUCCESS_LOG}" 'curl .*/api/v1/rules' 'Prometheus rule-health smoke check missing'

assert_contains "${TEST_ROOT}/success/stderr.log" 'inventory directory removed:' \
    'test hook did not report inventory cleanup'
inventory_dir=$(sed -n 's/^inventory directory removed: //p' "${TEST_ROOT}/success/stderr.log" | tail -1)
[[ -n "${inventory_dir}" && ! -e "${inventory_dir}" ]] || fail 'private inventory directory was not removed'
[[ "${inventory_dir}" == /tmp/* ]] || fail 'inventory directory was not created under the system temporary directory'

echo 'remote monitoring deployment contract tests passed'
