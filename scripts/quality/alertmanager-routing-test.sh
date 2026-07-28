#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CONFIG="${1:-${ROOT}/monitoring/remote/alertmanager/alertmanager.yaml}"
FIXTURE="${2:-${ROOT}/monitoring/remote/alertmanager/alertmanager-test.yaml}"
AMTOOL_IMAGE="${AMTOOL_IMAGE:-quay.io/prometheus/alertmanager:v0.33.1}"

go run "${ROOT}/scripts/quality/alertmanager-routing-test.go" "${CONFIG}" "${FIXTURE}"

run_route_test() {
    local expected="$1"
    shift
    if command -v amtool >/dev/null 2>&1; then
        timeout 30s amtool config routes test --config.file="${CONFIG}" \
            --verify.receivers="${expected}" "$@"
        return
    fi
    if ! command -v docker >/dev/null 2>&1; then
        echo 'amtool or docker is required for Alertmanager route contracts' >&2
        return 1
    fi
    timeout 60s docker run --rm \
        -v "$(dirname "${CONFIG}"):/config:ro" \
        --entrypoint amtool "${AMTOOL_IMAGE}" config routes test \
        --config.file="/config/$(basename "${CONFIG}")" \
        --verify.receivers="${expected}" "$@"
}

while IFS= read -r test_case; do
    name="$(jq -r '.name' <<<"${test_case}")"
    expected="$(jq -r '.expected | join(",")' <<<"${test_case}")"
    mapfile -t labels < <(jq -r '.input | to_entries[] | "\(.key)=\(.value)"' <<<"${test_case}")
    if ! run_route_test "${expected}" "${labels[@]}" >/dev/null; then
        echo "amtool route contract failed: ${name}" >&2
        exit 1
    fi
done < <(jq -c '.routing_tests[] | select(has("time") | not)' "${FIXTURE}")

echo 'Alertmanager routing contracts passed'
