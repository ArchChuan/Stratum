#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CONFIG="${1:-${ROOT}/monitoring/remote/alertmanager/alertmanager.yaml}"
FIXTURE="${2:-${ROOT}/monitoring/remote/alertmanager/alertmanager-test.yaml}"
AMTOOL_IMAGE="${AMTOOL_IMAGE:-quay.io/prometheus/alertmanager:v0.33.1}"

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

jq -e '
  type == "object" and
  (.routing_tests | type == "array" and length > 0) and
  (.inhibition_tests | type == "array" and length > 0) and
  all(.routing_tests[];
    (.name | type == "string" and length > 0) and
    (.input | type == "object" and all(.[]; type == "string")) and
    (.receivers | type == "array" and length == 1 and all(.[]; type == "string")) and
    ((has("time") | not) or (.time | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\\+08:00$")))) and
  all(.inhibition_tests[];
    (.name | type == "string" and length > 0) and
    (.policy | IN("public-component-cause", "deployment-pod-cause", "critical-warning-family")) and
    (.source | type == "object" and all(.[]; type == "string")) and
    (.target | type == "object" and all(.[]; type == "string")) and
    (.inhibited | type == "boolean"))
' "${FIXTURE}" >/dev/null

while IFS= read -r test_case; do
    name="$(jq -r '.name' <<<"${test_case}")"
    expected="$(jq -r '.receivers | join(",")' <<<"${test_case}")"
    mapfile -t labels < <(jq -r '.input | to_entries[] | "\(.key)=\(.value)"' <<<"${test_case}")
    if ! run_route_test "${expected}" "${labels[@]}" >/dev/null; then
        echo "route contract failed: ${name}" >&2
        exit 1
    fi
done < <(jq -c '.routing_tests[] | select(has("time") | not)' "${FIXTURE}")

route_at() {
    local input="$1"
    local timestamp="$2"
    local alertname severity weekday wall_time
    alertname="$(jq -r '.alertname // ""' <<<"${input}")"
    severity="$(jq -r '.severity // ""' <<<"${input}")"
    weekday="$(TZ=Asia/Shanghai date --date="${timestamp}" +%u)"
    wall_time="${timestamp:11:8}"

    if [[ "${alertname}" == "Watchdog" ]]; then
        printf 'null\n'
    elif [[ "${severity}" == "critical" ]]; then
        printf 'feishu-critical\n'
    elif [[ "${severity}" == "warning" && "${weekday}" -le 5 && \
        "${wall_time}" > "08:59:59" && "${wall_time}" < "19:00:00" ]]; then
        printf 'feishu-warning\n'
    else
        printf 'null\n'
    fi
}

while IFS= read -r test_case; do
    name="$(jq -r '.name' <<<"${test_case}")"
    timestamp="$(jq -r '.time' <<<"${test_case}")"
    expected="$(jq -r '.receivers[0]' <<<"${test_case}")"
    actual="$(route_at "$(jq -c '.input' <<<"${test_case}")" "${timestamp}")"
    if [[ "${actual}" != "${expected}" ]]; then
        echo "timed route contract failed: ${name}: expected ${expected}, got ${actual}" >&2
        exit 1
    fi
done < <(jq -c '.routing_tests[] | select(has("time"))' "${FIXTURE}")

label_equal() {
    local source="$1" target="$2" label="$3"
    [[ "$(jq -r --arg label "${label}" '.[$label] // ""' <<<"${source}")" == \
        "$(jq -r --arg label "${label}" '.[$label] // ""' <<<"${target}")" ]]
}

inhibited_by_policy() {
    local policy="$1" source="$2" target="$3" label component
    case "${policy}" in
        public-component-cause)
            [[ "$(jq -r '.alertname // ""' <<<"${source}")" == "StratumPublicEndpointDown" ]] || return 1
            [[ "$(jq -r '.service // ""' <<<"${target}")" == "stratum" ]] || return 1
            component="$(jq -r '.component // ""' <<<"${target}")"
            [[ -n "${component}" && ! "${component}" =~ ^(public-endpoint|prometheus|alertmanager|notification|blackbox)$ ]] || return 1
            for label in service environment; do label_equal "${source}" "${target}" "${label}" || return 1; done
            ;;
        deployment-pod-cause)
            [[ "$(jq -r '.alertname // ""' <<<"${source}")" =~ ^Stratum(Backend|Frontend)Unavailable$ ]] || return 1
            [[ "$(jq -r '.alertname // ""' <<<"${target}")" =~ ^StratumPod(RestartingFrequently|CrashLooping|PendingTooLong)$ ]] || return 1
            for label in service environment namespace deployment; do label_equal "${source}" "${target}" "${label}" || return 1; done
            ;;
        critical-warning-family)
            [[ "$(jq -r '.severity // ""' <<<"${source}")" == "critical" ]] || return 1
            [[ "$(jq -r '.severity // ""' <<<"${target}")" == "warning" ]] || return 1
            [[ -n "$(jq -r '.alert_family // ""' <<<"${source}")" ]] || return 1
            for label in service environment alert_family namespace deployment pod container persistentvolumeclaim node instance device mountpoint; do
                label_equal "${source}" "${target}" "${label}" || return 1
            done
            ;;
        *) return 1 ;;
    esac
}

while IFS= read -r test_case; do
    name="$(jq -r '.name' <<<"${test_case}")"
    expected="$(jq -r '.inhibited' <<<"${test_case}")"
    actual=false
    if inhibited_by_policy "$(jq -r '.policy' <<<"${test_case}")" \
        "$(jq -c '.source' <<<"${test_case}")" "$(jq -c '.target' <<<"${test_case}")"; then
        actual=true
    fi
    if [[ "${actual}" != "${expected}" ]]; then
        echo "inhibition contract failed: ${name}: expected ${expected}, got ${actual}" >&2
        exit 1
    fi
done < <(jq -c '.inhibition_tests[]' "${FIXTURE}")

echo 'Alertmanager routing contracts passed'
