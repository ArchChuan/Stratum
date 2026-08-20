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
    local pod="" active="" sent_first="" sent_second="" attempt=""

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
        if ! kill -0 "${pf_pid}" >/dev/null 2>&1; then
            echo "error: promtail port-forward exited unexpectedly" >&2
            exit 1
        fi
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
        active="$(curl -sf http://127.0.0.1:19090/metrics 2>/dev/null \
            | awk '$1 ~ /^promtail_files_active_total$/ {print $2; exit}' || true)"
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

    sent_first="$(curl -sf http://127.0.0.1:19090/metrics \
        | awk '$1 ~ /^promtail_sent_bytes_total/ && $2 ~ /^[0-9]+$/ {print $2; exit}')"
    for attempt in $(seq 1 6); do
        sleep 5
        sent_second="$(curl -sf http://127.0.0.1:19090/metrics \
            | awk '$1 ~ /^promtail_sent_bytes_total/ && $2 ~ /^[0-9]+$/ {print $2; exit}')"
        if [[ "${sent_second}" =~ ^[0-9]+$ && "${sent_second}" -gt "${sent_first:-0}" ]]; then
            break
        fi
    done
    if [[ ! "${sent_second}" =~ ^[0-9]+$ || "${sent_second}" -le "${sent_first:-0}" ]]; then
        echo "error: promtail_sent_bytes_total did not increase" >&2
        echo "hint: promtail is not pushing to Loki (sent ${sent_first:-0} -> ${sent_second:-0})" >&2
        exit 1
    fi

    echo "log flow verified: files_active=${active}, sent_bytes ${sent_first} -> ${sent_second}"
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
