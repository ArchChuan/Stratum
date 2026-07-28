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

run_validator() {
    CALLS="${CALLS}" ALERTMANAGER_ROUTING_TEST="${FAKE_BIN}/alertmanager-routing-test" \
        PATH="${FAKE_BIN}:${PATH}" bash "${VALIDATOR}"
}

run_validator >/dev/null

if ! grep -q '^routing ' "${CALLS}"; then
    echo 'validator did not invoke Alertmanager routing contracts' >&2
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
    if FAIL_TOOL="${failing_tool}" CALLS="${CALLS}" PATH="${FAKE_BIN}:${PATH}" \
        bash "${VALIDATOR}" >/dev/null 2>&1; then
        echo "validator swallowed ${failing_tool} failure" >&2
        exit 1
    fi
}

assert_fails helm
assert_fails promtool
assert_fails amtool
if FAIL_TOOL=routing CALLS="${CALLS}" ALERTMANAGER_ROUTING_TEST="${FAKE_BIN}/alertmanager-routing-test" \
    PATH="${FAKE_BIN}:${PATH}" bash "${VALIDATOR}" >/dev/null 2>&1; then
    echo 'validator swallowed routing contract failure' >&2
    exit 1
fi

echo 'Monitoring validator self-tests passed'
