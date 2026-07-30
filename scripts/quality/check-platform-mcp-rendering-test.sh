#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHART="${ROOT}/helm"
RENDERED="$(mktemp)"
trap 'rm -f "${RENDERED}"' EXIT

require_source() {
  local file="$1" pattern="$2" label="$3"
  grep -Eq -- "${pattern}" "${file}" || { echo "platform MCP rendering: missing ${label}" >&2; exit 1; }
}

require_source "${CHART}/templates/platform-mcp-service.yaml" 'type:[[:space:]]*ClusterIP' 'ClusterIP service'
require_source "${CHART}/templates/platform-mcp-serviceaccount.yaml" 'automountServiceAccountToken:[[:space:]]*false' 'disabled service account token mounting'
require_source "${CHART}/templates/internal-certificates.yaml" 'spiffe://stratum.local/ns/stratum/sa/stratum-backend' 'backend SPIFFE URI'
require_source "${CHART}/templates/internal-certificates.yaml" 'spiffe://stratum.local/ns/stratum/sa/stratum-platform-mcp' 'Platform MCP SPIFFE URI'
require_source "${CHART}/templates/internal-certificates.yaml" 'spiffe://stratum.local/ns/stratum/sa/stratum-platform-mcp-monitor' 'monitoring SPIFFE URI'
require_source "${CHART}/templates/platform-mcp-networkpolicy.yaml" 'port:[[:space:]]*8443' '8443-only application egress'
require_source "${CHART}/templates/platform-mcp-servicemonitor.yaml" 'stratum-platform-mcp-monitor-tls' 'mTLS metrics scrape'
require_source "${CHART}/templates/platform-mcp-prometheusrule.yaml" 'StratumPlatformMCPNoReadyReplicas' 'Platform MCP availability alert'
require_source "${ROOT}/grafana/platform-mcp-dashboard.json" 'platform_mcp_request_duration_seconds' 'Platform MCP Grafana dashboard'
for denied in 5432 6379 4222 19530; do
  if grep -Eq "port:[[:space:]]*${denied}" "${CHART}/templates/platform-mcp-networkpolicy.yaml"; then
    echo "platform MCP rendering: forbidden egress port ${denied}" >&2
    exit 1
  fi
done
require_source "${CHART}/values-prod.yaml" 'replicaCount:[[:space:]]*[2-9][0-9]*' 'production high availability'

if command -v helm >/dev/null 2>&1; then
  helm template stratum "${CHART}" --namespace stratum -f "${CHART}/values-prod.yaml" >"${RENDERED}"
  grep -Eq 'name:[[:space:]]*stratum-platform-mcp' "${RENDERED}"
  grep -Eq 'kind:[[:space:]]*ServiceMonitor' "${RENDERED}"
  grep -Eq 'kind:[[:space:]]*PrometheusRule' "${RENDERED}"
  if grep -Eq 'kind:[[:space:]]*Ingress([[:space:][:print:]]*stratum-platform-mcp)' "${RENDERED}"; then
    echo 'platform MCP rendering: public ingress is forbidden' >&2
    exit 1
  fi
fi

echo 'platform MCP rendering contract passed'
