#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
RUNNER="${ROOT}/scripts/quality/alertmanager-routing-test.sh"
CONFIG="${ROOT}/monitoring/remote/alertmanager/alertmanager.yaml"
FIXTURE="${ROOT}/monitoring/remote/alertmanager/alertmanager-test.yaml"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "${TMP_ROOT}"' EXIT

bash "${RUNNER}" "${CONFIG}" "${FIXTURE}" >/dev/null

sed '0,/name: cn-work-hours/s//name: changed-work-hours/' "${CONFIG}" >"${TMP_ROOT}/changed-interval.yaml"
if bash "${RUNNER}" "${TMP_ROOT}/changed-interval.yaml" "${FIXTURE}" >/dev/null 2>&1; then
    echo 'routing contracts ignored a changed active time interval' >&2
    exit 1
fi

sed '/^inhibit_rules:/,$c\inhibit_rules: []' "${CONFIG}" >"${TMP_ROOT}/removed-inhibitions.yaml"
if bash "${RUNNER}" "${TMP_ROOT}/removed-inhibitions.yaml" "${FIXTURE}" >/dev/null 2>&1; then
    echo 'routing contracts ignored removed inhibition rules' >&2
    exit 1
fi

echo 'Alertmanager routing contract self-tests passed'
