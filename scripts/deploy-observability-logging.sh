#!/usr/bin/env bash
# Apply the raw observability manifests (Loki/Promtail/Jaeger/OTel collector)
# and roll workloads whose mounted ConfigMap content changed.
#
# kubectl apply alone does not roll a DaemonSet/Deployment when a mounted
# ConfigMap changes: the old process keeps the stale config silently. Observed
# 2026-08: promtail tailed zero files for days after the __path__ glob fix was
# applied to promtail-config, because the running pod never reloaded. The
# checksum/config annotation makes the workload controller roll exactly when
# the ConfigMap content changes, and the final log-flow check fails closed so
# a silently broken pipeline cannot pass a deploy.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Global so the EXIT trap can reference it after verify_promtail_log_flow
# returns (a function-local would be unbound under set -u at trap time).
pf_pid=""

rollout_on_config_change() {
    local kind="$1" name="$2" namespace="$3" configmap="$4" config_key="$5"
    local raw="" sha="" annotation=""

    # go-template index is used because kubectl jsonpath bracket syntax
    # ({.data['key']}) does not handle keys containing dots/slashes on all
    # supported kubectl versions; the escaped form is version-fragile too.
    raw="$(kubectl get configmap "${configmap}" -n "${namespace}" \
        -o go-template="{{ index .data \"${config_key}\" }}" 2>/dev/null)" || {
        echo "error: cannot read configmap ${namespace}/${configmap} key ${config_key}" >&2
        exit 1
    }
    if [[ -z "${raw}" ]]; then
        echo "error: configmap ${namespace}/${configmap} key ${config_key} is empty" >&2
        exit 1
    fi
    sha="$(printf '%s' "${raw}" | sha256sum | awk '{print $1}')"

    annotation="$(kubectl get "${kind}" "${name}" -n "${namespace}" \
        -o go-template='{{ with .spec.template.metadata.annotations }}{{ index . "checksum/config" }}{{ end }}' 2>/dev/null || true)"
    if [[ "${sha}" == "${annotation}" ]]; then
        echo "config unchanged for ${kind}/${name} (${configmap}): no rollout needed"
        return
    fi
    echo "config change detected for ${kind}/${name} (${configmap}): rolling out with checksum ${sha}"
    kubectl patch "${kind}" "${name}" -n "${namespace}" --type=merge -p \
        "{\"spec\":{\"template\":{\"metadata\":{\"annotations\":{\"checksum/config\":\"${sha}\"}}}}}"
}

verify_promtail_log_flow() {
    local pod="" active="" sent_first="" sent_second="" attempt="" metric=""

    # Returns the numeric value of a promtail counter, or empty on a transient
    # fetch failure. Never fails the script: the caller retries and only fails
    # after a sustained problem, so one flaky curl (e.g. exit 56 through the
    # deploy runner tunnel) cannot abort a healthy deploy.
    promtail_metric() {
        curl -sf http://127.0.0.1:19090/metrics 2>/dev/null \
            | awk -v name="$1" '$1 ~ ("^" name) && $2 ~ /^[0-9]+$/ {print $2; exit}' \
            || true
    }

    port_forward_alive() {
        if ! kill -0 "${pf_pid}" >/dev/null 2>&1; then
            echo "error: promtail port-forward exited unexpectedly" >&2
            exit 1
        fi
    }

    pod="$(kubectl get pod -n monitoring -l app=promtail \
        -o jsonpath='{.items[0].metadata.name}')"
    if [[ -z "${pod}" ]]; then
        echo "error: no promtail pod found" >&2
        exit 1
    fi
    kubectl port-forward "pod/${pod}" -n monitoring 19090:9080 >/dev/null 2>&1 &
    pf_pid=$!
    trap 'if [[ -n "${pf_pid:-}" ]]; then
              kill "${pf_pid}" >/dev/null 2>&1 || true
              wait "${pf_pid}" >/dev/null 2>&1 || true
          fi' EXIT

    for attempt in $(seq 1 30); do
        port_forward_alive
        if curl -sf http://127.0.0.1:19090/ready >/dev/null 2>&1; then
            break
        fi
        sleep 2
    done
    if ! curl -sf http://127.0.0.1:19090/ready >/dev/null 2>&1; then
        echo "error: promtail did not become ready through port-forward" >&2
        exit 1
    fi

    for attempt in $(seq 1 30); do
        port_forward_alive
        active="$(promtail_metric promtail_files_active_total)"
        if [[ "${active}" =~ ^[0-9]+$ && "${active}" -gt 0 ]]; then
            break
        fi
        sleep 2
    done
    if [[ ! "${active}" =~ ^[0-9]+$ || "${active}" -le 0 ]]; then
        echo "error: promtail_files_active_total=${active}, expected > 0" >&2
        echo "hint: StratumPromtailNoActiveFiles fires on this condition" >&2
        exit 1
    fi

    for attempt in $(seq 1 5); do
        port_forward_alive
        sent_first="$(promtail_metric promtail_sent_bytes_total)"
        if [[ "${sent_first}" =~ ^[0-9]+$ ]]; then
            break
        fi
        sleep 2
    done
    if [[ ! "${sent_first}" =~ ^[0-9]+$ ]]; then
        echo "error: cannot read promtail_sent_bytes_total (5 attempts)" >&2
        exit 1
    fi

    # A quiet cluster can legitimately produce no new log lines for a minute,
    # so a flat counter is not a broken pipeline. Promtail counters start at 0
    # on pod start: any value > 0 proves at least one successful push to Loki.
    if [[ "${sent_first}" -gt 0 ]]; then
        echo "log flow verified: files_active=${active}, sent_bytes=${sent_first} (promtail is pushing to Loki)"
        return
    fi

    for attempt in $(seq 1 12); do
        sleep 5
        port_forward_alive
        sent_second="$(promtail_metric promtail_sent_bytes_total)"
        if [[ "${sent_second}" =~ ^[0-9]+$ && "${sent_second}" -gt 0 ]]; then
            echo "log flow verified: files_active=${active}, sent_bytes ${sent_first} -> ${sent_second}"
            return
        fi
    done
    echo "error: promtail_sent_bytes_total stayed 0 after 60s" >&2
    echo "hint: promtail is not pushing to Loki (files_active=${active})" >&2
    exit 1
}

kubectl apply -f "${ROOT}/k8s/logging.yaml"
kubectl apply -f "${ROOT}/k8s/tracing.yaml"

# Workloads that mount a ConfigMap get rolled when its content changes. Jaeger
# has no config file, so it is skipped; its rollout status is still awaited.
rollout_on_config_change deployment loki monitoring loki-config loki.yaml
rollout_on_config_change daemonset promtail monitoring promtail-config promtail.yaml
rollout_on_config_change deployment otel-collector stratum otel-collector-config config.yaml

kubectl rollout status deployment/loki -n monitoring --timeout=5m
kubectl rollout status daemonset/promtail -n monitoring --timeout=5m
kubectl rollout status deployment/jaeger -n monitoring --timeout=5m
kubectl rollout status deployment/otel-collector -n stratum --timeout=5m

verify_promtail_log_flow
