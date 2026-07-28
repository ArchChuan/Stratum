#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VALIDATOR="${ROOT}/scripts/quality/monitoring-config-test.sh"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "${TMP_ROOT}"' EXIT

bash "${ROOT}/scripts/quality/alertmanager-routing-test-test.sh" >/dev/null

FAKE_BIN="${TMP_ROOT}/bin"
CALLS="${TMP_ROOT}/calls.log"
mkdir -p "${FAKE_BIN}"

for tool in helm promtool amtool; do
    cat >"${FAKE_BIN}/${tool}" <<'TOOL'
#!/usr/bin/env bash
set -euo pipefail
name="$(basename "$0")"
printf '%s %s\n' "${name}" "$*" >>"${CALLS:?}"
if [[ "${FAIL_TOOL:-}" == "${name}" ]]; then
    exit 17
fi
if [[ "${name}" == "helm" ]]; then
    if [[ "$*" == *"kube-prometheus-stack"* ]]; then
        if [[ "${OMIT_ALERTMANAGER_CONFIG:-}" == "1" ]]; then
            exit 0
        fi
        if [[ "${DRIFT_CHART_CONFIG:-}" == "1" ]]; then
            encoded="$(printf 'route:\n  receiver: drift\nreceivers:\n  - name: drift\n' | base64 -w0)"
        else
            encoded="$(base64 -w0 "${TEST_ROOT:?}/monitoring/remote/alertmanager/alertmanager.yaml")"
        fi
        kind=Secret
        secret_name=alertmanager-kps-kube-prometheus-stack-alertmanager
        if [[ "${WRONG_ALERTMANAGER_KIND:-}" == "1" ]]; then kind=ConfigMap; fi
        if [[ "${WRONG_ALERTMANAGER_NAME:-}" == "1" ]]; then secret_name=wrong-alertmanager; fi
        printf 'apiVersion: v1\nkind: %s\nmetadata:\n  name: %s\ndata:\n  alertmanager.yaml: "%s"\n' \
            "${kind}" "${secret_name}" "${encoded}"
        if [[ "${DUPLICATE_ALERTMANAGER_SECRET:-}" == "1" ]]; then
            printf '%s\napiVersion: v1\nkind: Secret\nmetadata:\n  name: %s\ndata:\n  alertmanager.yaml: "%s"\n' \
                '---' 'alertmanager-kps-kube-prometheus-stack-alertmanager' "${encoded}"
        fi
        exit 0
    fi
    cat <<'YAML'
replacement: remote-test
targetLabel: environment
replacement: stratum
targetLabel: service
YAML
fi
TOOL
    chmod +x "${FAKE_BIN}/${tool}"
done

cat >"${FAKE_BIN}/alertmanager-routing-test" <<'TOOL'
#!/usr/bin/env bash
set -euo pipefail
printf 'routing %s\n' "$*" >>"${CALLS:?}"
if [[ "${FAIL_TOOL:-}" == "routing" ]]; then
    exit 17
fi
TOOL
chmod +x "${FAKE_BIN}/alertmanager-routing-test"

cat >"${FAKE_BIN}/monitoring-dashboard-test" <<'TOOL'
#!/usr/bin/env bash
set -euo pipefail
printf 'dashboards %s\n' "$*" >>"${CALLS:?}"
if [[ "${FAIL_TOOL:-}" == "dashboards" ]]; then
    exit 17
fi
TOOL
chmod +x "${FAKE_BIN}/monitoring-dashboard-test"

run_validator() {
    CALLS="${CALLS}" TEST_ROOT="${ROOT}" \
        ALERTMANAGER_ROUTING_TEST="${FAKE_BIN}/alertmanager-routing-test" \
        MONITORING_DASHBOARD_TEST="${FAKE_BIN}/monitoring-dashboard-test" \
        PATH="${FAKE_BIN}:${PATH}" bash "${VALIDATOR}"
}

run_validator >/dev/null

if ! grep -q '^routing ' "${CALLS}"; then
    echo 'validator did not invoke Alertmanager routing contracts' >&2
    exit 1
fi
if ! grep -q '^dashboards ' "${CALLS}"; then
    echo 'validator did not invoke dashboard contracts' >&2
    exit 1
fi

RUNBOOK_FILE="${ROOT}/docs/operations/alerts/availability.md"
if [[ -f "${RUNBOOK_FILE}" ]]; then
    mkdir -p "${TMP_ROOT}/runbook-root/docs/operations"
    cp -R "${ROOT}/docs/operations/." "${TMP_ROOT}/runbook-root/docs/operations/"
    sed -i 's/^## StratumPublicEndpointDown$/## WrongAlertHeading/' \
        "${TMP_ROOT}/runbook-root/docs/operations/alerts/availability.md"
    if RUNBOOK_ROOT="${TMP_ROOT}/runbook-root" run_validator >/dev/null 2>&1; then
        echo 'validator accepted a runbook without the corresponding alert heading' >&2
        exit 1
    fi
fi

mkdir -p "${TMP_ROOT}/rules-missing-url"
cp "${ROOT}"/monitoring/remote/rules/*.yaml "${TMP_ROOT}/rules-missing-url/"
sed -i '/runbook_url: .*public-endpoint-down/d' \
    "${TMP_ROOT}/rules-missing-url/stratum-availability.yaml"
if MONITORING_RULES_DIR="${TMP_ROOT}/rules-missing-url" run_validator >/dev/null 2>&1; then
    echo 'validator accepted an alert without a runbook_url' >&2
    exit 1
fi

mkdir -p "${TMP_ROOT}/rules-duplicate-url"
cp "${ROOT}"/monitoring/remote/rules/*.yaml "${TMP_ROOT}/rules-duplicate-url/"
sed -i '/runbook_url: .*public-endpoint-down/a\          runbook_url: /docs/operations/alerts/availability.md#public-endpoint-down' \
    "${TMP_ROOT}/rules-duplicate-url/stratum-availability.yaml"
if MONITORING_RULES_DIR="${TMP_ROOT}/rules-duplicate-url" run_validator >/dev/null 2>&1; then
    echo 'validator accepted an alert with duplicate runbook_url annotations' >&2
    exit 1
fi

for expected in helm promtool amtool; do
    if ! grep -q "^${expected} " "${CALLS}"; then
        echo "validator did not invoke ${expected}" >&2
        exit 1
    fi
done

assert_fails() {
    local failing_tool="$1"
    if FAIL_TOOL="${failing_tool}" CALLS="${CALLS}" TEST_ROOT="${ROOT}" \
        ALERTMANAGER_ROUTING_TEST="${FAKE_BIN}/alertmanager-routing-test" \
        MONITORING_DASHBOARD_TEST="${FAKE_BIN}/monitoring-dashboard-test" \
        PATH="${FAKE_BIN}:${PATH}" bash "${VALIDATOR}" >/dev/null 2>&1; then
        echo "validator swallowed ${failing_tool} failure" >&2
        exit 1
    fi
}

assert_fails helm
assert_fails promtool
assert_fails amtool
if DRIFT_CHART_CONFIG=1 run_validator >/dev/null 2>&1; then
    echo 'validator accepted drift between chart and standalone Alertmanager config' >&2
    exit 1
fi
if OMIT_ALERTMANAGER_CONFIG=1 run_validator >/dev/null 2>&1; then
    echo 'validator accepted a chart render without Alertmanager config' >&2
    exit 1
fi
if WRONG_ALERTMANAGER_KIND=1 run_validator >/dev/null 2>&1; then
    echo 'validator accepted Alertmanager data from a non-Secret object' >&2
    exit 1
fi
if WRONG_ALERTMANAGER_NAME=1 run_validator >/dev/null 2>&1; then
    echo 'validator accepted Alertmanager data from the wrong Secret' >&2
    exit 1
fi
if DUPLICATE_ALERTMANAGER_SECRET=1 run_validator >/dev/null 2>&1; then
    echo 'validator accepted duplicate Alertmanager Secrets' >&2
    exit 1
fi
if FAIL_TOOL=routing CALLS="${CALLS}" TEST_ROOT="${ROOT}" \
    ALERTMANAGER_ROUTING_TEST="${FAKE_BIN}/alertmanager-routing-test" \
    PATH="${FAKE_BIN}:${PATH}" bash "${VALIDATOR}" >/dev/null 2>&1; then
    echo 'validator swallowed routing contract failure' >&2
    exit 1
fi
if FAIL_TOOL=dashboards CALLS="${CALLS}" TEST_ROOT="${ROOT}" \
    ALERTMANAGER_ROUTING_TEST="${FAKE_BIN}/alertmanager-routing-test" \
    MONITORING_DASHBOARD_TEST="${FAKE_BIN}/monitoring-dashboard-test" \
    PATH="${FAKE_BIN}:${PATH}" bash "${VALIDATOR}" >/dev/null 2>&1; then
    echo 'validator swallowed dashboard contract failure' >&2
    exit 1
fi

echo 'Monitoring validator self-tests passed'
