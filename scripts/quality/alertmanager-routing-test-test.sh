#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
RUNNER="${ROOT}/scripts/quality/alertmanager-routing-test.sh"
CONFIG="${ROOT}/monitoring/remote/alertmanager/alertmanager.yaml"
FIXTURE="${ROOT}/monitoring/remote/alertmanager/alertmanager-test.yaml"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "${TMP_ROOT}"' EXIT

bash "${RUNNER}" "${CONFIG}" "${FIXTURE}" >/dev/null

assert_mutation_fails() {
    local config="$1"
    local message="$2"
    if bash "${RUNNER}" "${config}" "${FIXTURE}" >/dev/null 2>&1; then
        echo "routing contracts ignored ${message}" >&2
        exit 1
    fi
}

sed '0,/receiver: feishu-critical/s//receiver: null/' "${CONFIG}" >"${TMP_ROOT}/changed-receiver.yaml"
assert_mutation_fails "${TMP_ROOT}/changed-receiver.yaml" 'a changed critical receiver'

sed 's/end_time: "19:00"/end_time: "18:00"/' "${CONFIG}" >"${TMP_ROOT}/changed-warning-boundary.yaml"
assert_mutation_fails "${TMP_ROOT}/changed-warning-boundary.yaml" 'a changed warning boundary'

sed '0,/name: cn-work-hours/s//name: changed-work-hours/' "${CONFIG}" >"${TMP_ROOT}/changed-interval.yaml"
assert_mutation_fails "${TMP_ROOT}/changed-interval.yaml" 'a changed active time interval'

sed '/^inhibit_rules:/,$c\inhibit_rules: []' "${CONFIG}" >"${TMP_ROOT}/removed-inhibitions.yaml"
assert_mutation_fails "${TMP_ROOT}/removed-inhibitions.yaml" 'removed inhibition rules'

sed '/^      - deployment$/d' "${CONFIG}" >"${TMP_ROOT}/removed-deployment-equal.yaml"
assert_mutation_fails "${TMP_ROOT}/removed-deployment-equal.yaml" 'a removed inhibition equality label'

sed 's/public-endpoint|prometheus|alertmanager/public-endpoint|alertmanager/' \
    "${CONFIG}" >"${TMP_ROOT}/changed-inhibit-matcher.yaml"
assert_mutation_fails "${TMP_ROOT}/changed-inhibit-matcher.yaml" 'a changed inhibition matcher'

sed '/location: Asia\/Shanghai/a\        months:\n          - january' \
    "${CONFIG}" >"${TMP_ROOT}/unsupported-calendar.yaml"
assert_mutation_fails "${TMP_ROOT}/unsupported-calendar.yaml" 'an unsupported calendar interval'

sed '/end_time: "19:00"/a\          - start_time: "23:00"\n            end_time: "01:00"' \
    "${CONFIG}" >"${TMP_ROOT}/overnight-range.yaml"
assert_mutation_fails "${TMP_ROOT}/overnight-range.yaml" 'an unsupported overnight range'

sed '0,/receiver: "null"/s//receiver: "null"\n  mute_time_intervals:\n    - cn-work-hours/' \
    "${CONFIG}" >"${TMP_ROOT}/unsupported-route.yaml"
assert_mutation_fails "${TMP_ROOT}/unsupported-route.yaml" 'an unsupported route construct'

echo 'Alertmanager routing contract self-tests passed'
