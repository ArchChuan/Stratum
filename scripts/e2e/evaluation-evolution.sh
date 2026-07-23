#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
run_id="e2e-evolution-$(date +%s)-$$"
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/stratum-evolution.XXXXXX")
backend_log="$work_dir/backend.log"
frontend_log="$work_dir/frontend.log"
project="stratum-evolution-${run_id//[^a-zA-Z0-9]/}"
backend_pid=''
frontend_pid=''
mcp_pid=''
llm_pid=''
milvus_proxy_pid=''

stop_process_group() {
  local pid=$1 label=$2
  [[ -n "$pid" ]] || return 0
  kill -TERM -- "-$pid" 2>/dev/null || true
  for ((attempt=1; attempt<=50; attempt++)); do
    if ! kill -0 -- "-$pid" 2>/dev/null; then
      wait "$pid" 2>/dev/null || true
      return 0
    fi
    sleep 0.1
  done
  printf 'evaluation-evolution cleanup: force-stopping %s process group\n' "$label" >&2
  kill -KILL -- "-$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
}

bounded_compose_down() {
  local label=$1 compose_project=$2
  shift 2
  local compose=(docker compose -p "$compose_project" "$@")
  local container_ids=() remaining_ids=()
  if ! timeout 60 "${compose[@]}" down -v --remove-orphans >/dev/null 2>&1; then
    printf 'evaluation-evolution cleanup: %s compose down exceeded 60s; force-removing exact project containers\n' \
      "$label" >&2
    mapfile -t container_ids < <("${compose[@]}" ps -aq 2>/dev/null)
    if (( ${#container_ids[@]} > 0 )); then
      timeout 30 docker rm -f "${container_ids[@]}" >/dev/null 2>&1 || \
        printf 'evaluation-evolution cleanup: %s exact container removal did not finish within 30s\n' "$label" >&2
    fi
    timeout 30 "${compose[@]}" down -v --remove-orphans >/dev/null 2>&1 || \
      printf 'evaluation-evolution cleanup: %s network or volume cleanup remained incomplete\n' "$label" >&2
  fi
  mapfile -t remaining_ids < <(docker ps -aq --filter "label=com.docker.compose.project=$compose_project")
  if (( ${#remaining_ids[@]} > 0 )); then
    timeout 30 docker rm -f "${remaining_ids[@]}" >/dev/null 2>&1 || true
  fi
  mapfile -t remaining_ids < <(docker ps -aq --filter "label=com.docker.compose.project=$compose_project")
  if (( ${#remaining_ids[@]} > 0 )); then
    printf 'evaluation-evolution cleanup: %s project still has %d container(s)\n' \
      "$label" "${#remaining_ids[@]}" >&2
    return 1
  fi
}

cleanup_resources() {
  local cleanup_status=0
  set +e
  stop_process_group "$frontend_pid" frontend
  stop_process_group "$backend_pid" backend
  stop_process_group "$mcp_pid" mcp
  stop_process_group "$llm_pid" llm
  stop_process_group "$milvus_proxy_pid" milvus-proxy
  if [[ -f "$work_dir/compose.yml" ]]; then
    bounded_compose_down base "$project" -f "$work_dir/compose.yml" || cleanup_status=1
  fi
  if [[ -n "${E2E_OPIK_COMPOSE_FILE:-}" && -f "${E2E_OPIK_COMPOSE_FILE:-}" ]]; then
    bounded_compose_down opik "${project}-opik" -f "$E2E_OPIK_COMPOSE_FILE" \
      -f "$work_dir/opik-override.yml" || cleanup_status=1
  fi
  rm -rf "$work_dir"
  return "$cleanup_status"
}

cleanup_on_exit() {
  local status=$?
  trap - EXIT INT TERM
  cleanup_resources || {
    (( status == 0 )) && status=1
  }
  exit "$status"
}
trap cleanup_on_exit EXIT
trap 'exit 130' INT TERM

fail() { printf 'evaluation-evolution E2E: %s\n' "$1" >&2; exit 1; }
collector_diagnostics() {
  local collector_id collector_state collector_health collector_mapping
  collector_id=$(docker compose -p "$project" -f "$work_dir/compose.yml" ps -q otel 2>/dev/null)
  [[ -n "$collector_id" ]] || return 0
  collector_state=$(docker inspect -f '{{.State.Status}}' "$collector_id" 2>/dev/null || true)
  collector_health=$(docker inspect -f \
    '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$collector_id" 2>/dev/null || true)
  collector_mapping=$(docker compose -p "$project" -f "$work_dir/compose.yml" port otel 4317 2>/dev/null || true)
  printf 'collector-state: id=%s status=%s health=%s mapping=%s allocated-port=%s\n' \
    "${collector_id:0:12}" "$collector_state" "$collector_health" "$collector_mapping" "${otel_port:-unset}"
  if [[ -n "${otel_port:-}" ]]; then
    if ss -H -ltn "sport = :$otel_port" 2>/dev/null | rg -q .; then
      printf 'collector-host-listener: port=%s listening=true\n' "$otel_port"
    else
      printf 'collector-host-listener: port=%s listening=false\n' "$otel_port"
    fi
  fi
  docker logs "$collector_id" >"$work_dir/collector.log" 2>&1
  python3 - "$work_dir/collector.log" <<'PY'
import os, re, sys

secret = os.environ.get('OPENAI_API_KEY', '')
for raw in open(sys.argv[1], encoding='utf-8', errors='replace'):
    lower = raw.lower()
    if not any(marker in lower for marker in ('exporter', 'exporting failed', 'status code', 'otlphttp')):
        continue
    if any(marker in lower for marker in ('raw payload', 'upstream body', 'upstream response')):
        continue
    line = raw.rstrip()
    if secret:
        line = line.replace(secret, '[REDACTED]')
    line = re.sub(r'Bearer\s+[^\s,;}]+', 'Bearer [REDACTED]', line, flags=re.I)
    line = re.sub(r'(?i)(api[_-]?key|access[_-]?token|credential|secret)([=: ]+)[^ ,;}]+', r'\1\2[REDACTED]', line)
    print('collector-exporter:', line)
PY
  if [[ -n "${otel_metrics_port:-}" ]]; then
    curl -fsS "http://127.0.0.1:${otel_metrics_port}/metrics" 2>/dev/null | \
      rg '^otelcol_exporter_(sent|send_failed)_spans' | sed 's/^/collector-metric: /' || true
  fi
}
assert_collector_binding() {
  local actual_id actual_mapping
  actual_id=$(docker compose -p "$project" -f "$work_dir/compose.yml" ps -q otel 2>/dev/null)
  actual_mapping=$(docker compose -p "$project" -f "$work_dir/compose.yml" port otel 4317 2>/dev/null)
  [[ "$actual_id" == "$otel_cid" ]] || fail 'Collector container changed during the server lifecycle'
  [[ "$actual_mapping" == "127.0.0.1:${otel_port}" ]] || \
    fail "Collector OTLP mapping changed during the server lifecycle: expected 127.0.0.1:${otel_port}, got $actual_mapping"
}
poll() {
  local description=$1 command=$2 attempts=${3:-90}
  for ((i=1; i<=attempts; i++)); do
    if bash -c "$command" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  fail "timed out waiting for $description"
}
opik_readiness_diagnostics() {
  local backend_cid backend_state health_code dependency cid status health connectivity
  backend_cid=$("${opik_compose[@]}" ps -q backend 2>/dev/null || true)
  [[ -n "$backend_cid" ]] || backend_cid=$("${opik_compose[@]}" ps -aq backend 2>/dev/null || true)
  if [[ -z "$backend_cid" ]]; then
    printf 'opik-diagnostic: backend-container=missing\n' >&2
    return 0
  fi
  backend_state=$(docker inspect -f \
    'id={{.Id}} status={{.State.Status}} running={{.State.Running}} restart-count={{.RestartCount}} health={{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}} exit={{.State.ExitCode}} oom={{.State.OOMKilled}}' \
    "$backend_cid" 2>/dev/null || true)
  printf 'opik-backend-state: %s\n' "$backend_state" >&2
  health_code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 3 \
    "http://127.0.0.1:${E2E_OPIK_BACKEND_PORT}/health-check" 2>/dev/null || true)
  printf 'opik-health-check: http-status=%s\n' "${health_code:-unreachable}" >&2

  for dependency in mysql clickhouse; do
    cid=$("${opik_compose[@]}" ps -q "$dependency" 2>/dev/null || true)
    if [[ -z "$cid" ]]; then
      printf 'opik-dependency: service=%s container=missing\n' "$dependency" >&2
      continue
    fi
    read -r status health < <(docker inspect -f \
      '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$cid" 2>/dev/null)
    printf 'opik-dependency: service=%s id=%s status=%s health=%s\n' \
      "$dependency" "${cid:0:12}" "$status" "$health" >&2
  done

  for dependency in 'mysql 3306' 'clickhouse 8123'; do
    read -r dependency port <<<"$dependency"
    connectivity=$(docker exec "$backend_cid" sh -c \
      "if command -v bash >/dev/null 2>&1; then timeout 3 bash -c '</dev/tcp/$dependency/$port'; else exit 127; fi" \
      >/dev/null 2>&1 && printf reachable || printf unavailable)
    printf 'opik-backend-connectivity: target=%s:%s status=%s\n' \
      "$dependency" "$port" "$connectivity" >&2
  done

  docker logs --tail 300 "$backend_cid" >"$work_dir/opik-backend-diagnostic.log" 2>&1 || true
  python3 - "$work_dir/opik-backend-diagnostic.log" <<'PY'
import os, re, sys

known_secret = os.environ.get('OPENAI_API_KEY', '')
markers = ('startup', 'started', 'migration', 'migrat', 'health', 'error', 'exception', 'failed', 'jdbc')
for raw in open(sys.argv[1], encoding='utf-8', errors='replace'):
    line = raw.rstrip()
    if not any(marker in line.lower() for marker in markers):
        continue
    if known_secret:
        line = line.replace(known_secret, '[REDACTED]')
    line = re.sub(r'(?i)jdbc:[^\s,;}]+', 'jdbc:[REDACTED]', line)
    line = re.sub(r'(?i)Bearer\s+[^\s,;}]+', 'Bearer [REDACTED]', line)
    line = re.sub(
        r'(?i)(api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|secret|credential)'
        r'([=: ]+)[^ ,;}]+',
        r'\1\2[REDACTED]',
        line,
    )
    print('opik-backend-log:', line[:500], file=sys.stderr)
PY
}
poll_opik() {
  for ((attempt=1; attempt<=300; attempt++)); do
    if curl -fsS --max-time 3 "http://127.0.0.1:${E2E_OPIK_BACKEND_PORT}/health-check" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  opik_readiness_diagnostics
  fail 'timed out waiting for Opik'
}
wait_container_success() {
  local service=$1 cid status exit_code
  cid=$("${opik_compose[@]}" ps -aq "$service")
  [[ -n "$cid" ]] || fail "no container found for Opik initializer $service"
  for ((i=1; i<=120; i++)); do
    read -r status exit_code < <(docker inspect -f '{{.State.Status}} {{.State.ExitCode}}' "$cid")
    if [[ "$status" == exited ]]; then
      [[ "$exit_code" == 0 ]] || fail "Opik initializer $service exited with status $exit_code"
      return 0
    fi
    [[ "$status" == running || "$status" == created ]] || fail "Opik initializer $service entered $status"
    sleep 1
  done
  fail "timed out waiting for Opik initializer $service"
}
wait_container_healthy() {
  local service=$1 required=${2:-3} cid status health consecutive=0
  cid=$("${opik_compose[@]}" ps -q "$service")
  [[ -n "$cid" ]] || fail "no running container found for Opik service $service"
  for ((i=1; i<=300; i++)); do
    read -r status health < <(docker inspect -f \
      '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$cid")
    if [[ "$status" == running && "$health" == healthy ]]; then
      ((consecutive+=1))
      (( consecutive >= required )) && return 0
    else
      consecutive=0
    fi
    [[ "$status" == running ]] || fail "Opik service $service entered $status"
    sleep 1
  done
  fail "timed out waiting for healthy Opik service $service"
}

command -v docker >/dev/null || fail 'docker is required'
command -v openssl >/dev/null || fail 'openssl is required'
[[ -n "${OPENAI_API_KEY:-}" ]] || fail 'a test LLM key is required in OPENAI_API_KEY'
[[ -n "${E2E_OPIK_COMPOSE_FILE:-}" && -f "${E2E_OPIK_COMPOSE_FILE}" ]] || \
  fail 'E2E_OPIK_COMPOSE_FILE must point to a reviewed, pinned upstream Opik self-hosting compose file'
[[ -n "${E2E_OPIK_VERSION:-}" ]] || fail 'E2E_OPIK_VERSION is required for evidence'

export OPIK_VERSION="$E2E_OPIK_VERSION" NGINX_PORT=${E2E_OPIK_PORT:-15174}
if [[ -z "${E2E_OPIK_BACKEND_PORT:-}" ]]; then
  E2E_OPIK_BACKEND_PORT=$(python3 -c \
    'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
fi
export E2E_OPIK_BACKEND_PORT
cat >"$work_dir/otel.yml" <<YAML
receivers:
  otlp:
    protocols:
      grpc: { endpoint: 0.0.0.0:4317 }
processors:
  batch: {}
exporters:
  otlphttp/opik:
    endpoint: http://backend:8080/v1/private/otel
    headers:
      projectName: Default Project
  debug: { verbosity: basic }
extensions:
  health_check: { endpoint: 0.0.0.0:13133 }
service:
  extensions: [health_check]
  telemetry:
    metrics:
      address: 0.0.0.0:8888
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlphttp/opik, debug]
YAML

cat >"$work_dir/opik-override.yml" <<YAML
services:
  clickhouse:
    volumes:
      - "$work_dir/clickhouse-listen.xml:/etc/clickhouse-server/config.d/stratum-e2e-listen.xml:ro"
  backend:
    ports: ["127.0.0.1:${E2E_OPIK_BACKEND_PORT}:8080"]
    environment:
      TOGGLE_OPIK_AI_ENABLED: "false"
      TOGGLE_GUARDRAILS_ENABLED: "false"
      PYTHON_EVALUATOR_URL: "http://127.0.0.1:1"
YAML
cat >"$work_dir/clickhouse-listen.xml" <<'XML'
<clickhouse>
  <listen_host>0.0.0.0</listen_host>
</clickhouse>
XML

cat >"$work_dir/compose.yml" <<YAML
services:
  postgres:
    image: postgres:16.4-alpine
    environment:
      POSTGRES_USER: stratum_e2e
      POSTGRES_PASSWORD: stratum_e2e
      POSTGRES_DB: stratum_e2e
    ports: ["127.0.0.1::5432"]
    healthcheck: { test: ["CMD-SHELL", "pg_isready -U stratum_e2e"], interval: 1s, timeout: 2s, retries: 60 }
  nats:
    image: nats:2.10.25-alpine
    command: ["-js"]
    ports: ["127.0.0.1::4222"]
    healthcheck: { test: ["CMD", "nats-server", "--version"], interval: 2s, timeout: 2s, retries: 30 }
  redis:
    image: redis:7.2.4-alpine
    command: ["redis-server", "--save", ""]
    ports: ["127.0.0.1::6379"]
    healthcheck: { test: ["CMD", "redis-cli", "ping"], interval: 1s, timeout: 2s, retries: 30 }
  minio:
    image: minio/minio:RELEASE.2024-07-16T23-46-41Z
    command: server /data
    environment: { MINIO_ROOT_USER: e2e-minio, MINIO_ROOT_PASSWORD: e2e-minio-secret }
    ports: ["127.0.0.1::9000"]
  etcd:
    image: quay.io/coreos/etcd:v3.5.16
    command: ["etcd", "--advertise-client-urls=http://etcd:2379", "--listen-client-urls=http://0.0.0.0:2379", "--data-dir=/etcd"]
    healthcheck: { test: ["CMD", "etcdctl", "endpoint", "health"], interval: 2s, timeout: 3s, retries: 60 }
  milvus:
    image: milvusdb/milvus:v2.4.15
    command: ["milvus", "run", "standalone"]
    environment:
      ETCD_ENDPOINTS: etcd:2379
      MINIO_ADDRESS: minio:9000
      MINIO_ACCESS_KEY_ID: e2e-minio
      MINIO_SECRET_ACCESS_KEY: e2e-minio-secret
    depends_on:
      etcd: { condition: service_healthy }
      minio: { condition: service_started }
    ports: ["127.0.0.1::19530", "127.0.0.1::9091"]
    healthcheck: { test: ["CMD", "curl", "-f", "http://localhost:9091/healthz"], interval: 2s, timeout: 5s, retries: 90 }
  otel:
    image: otel/opentelemetry-collector-contrib:0.96.0
    command: ["--config=/etc/otelcol-contrib/config.yaml"]
    volumes: ["$work_dir/otel.yml:/etc/otelcol-contrib/config.yaml:ro"]
    extra_hosts: ["host.docker.internal:host-gateway"]
    ports: ["127.0.0.1::4317", "127.0.0.1::13133", "127.0.0.1::8888"]
    healthcheck: { test: ["CMD", "/otelcol-contrib", "components"], interval: 2s, timeout: 5s, retries: 30 }
YAML

docker compose -p "$project" -f "$work_dir/compose.yml" up -d --wait postgres nats redis minio etcd otel
pg_port=$(docker compose -p "$project" -f "$work_dir/compose.yml" port postgres 5432 | awk -F: '{print $NF}')
nats_port=$(docker compose -p "$project" -f "$work_dir/compose.yml" port nats 4222 | awk -F: '{print $NF}')
redis_port=$(docker compose -p "$project" -f "$work_dir/compose.yml" port redis 6379 | awk -F: '{print $NF}')
minio_port=$(docker compose -p "$project" -f "$work_dir/compose.yml" port minio 9000 | awk -F: '{print $NF}')
otel_port=$(docker compose -p "$project" -f "$work_dir/compose.yml" port otel 4317 | awk -F: '{print $NF}')
otel_metrics_port=$(docker compose -p "$project" -f "$work_dir/compose.yml" port otel 8888 | awk -F: '{print $NF}')
export E2E_OTEL_METRICS_PORT="$otel_metrics_port"
export TEST_DATABASE_URL="postgres://stratum_e2e:stratum_e2e@127.0.0.1:${pg_port}/stratum_e2e?sslmode=disable"
export POSTGRES_URL="$TEST_DATABASE_URL"
export STRATUM_TEST_POSTGRES_URL="$TEST_DATABASE_URL"
export E2E_POSTGRES_CONTAINER
E2E_POSTGRES_CONTAINER=$(docker compose -p "$project" -f "$work_dir/compose.yml" ps -q postgres)
[[ -n "$E2E_POSTGRES_CONTAINER" ]] || fail 'isolated PostgreSQL container ID is unavailable'
export NATS_URL="nats://127.0.0.1:${nats_port}"
export REDIS_URL="redis://127.0.0.1:${redis_port}"
export E2E_COMPOSE_PROJECT="$project"
export E2E_COMPOSE_FILE="$work_dir/compose.yml"

# Opik is deliberately its own reviewed upstream project; never replace it with Jaeger.
export MINIO_ROOT_USER="opik-${run_id}" MINIO_ROOT_PASSWORD="opik-${run_id}-isolated-secret"
opik_services=(mysql redis zookeeper clickhouse minio backend)
for excluded in python-backend frontend demo-data-generator guardrails-backend otel-collector; do
  [[ " ${opik_services[*]} " != *" $excluded "* ]] || fail "excluded Opik service selected: $excluded"
done
opik_compose=(docker compose -p "${project}-opik" -f "$E2E_OPIK_COMPOSE_FILE" -f "$work_dir/opik-override.yml")
export E2E_OPIK_PROJECT="${project}-opik"
export E2E_OPIK_OVERRIDE_FILE="$work_dir/opik-override.yml"
"${opik_compose[@]}" up -d --wait --no-build mysql redis zookeeper minio
"${opik_compose[@]}" up -d --no-build clickhouse-init
wait_container_success clickhouse-init
clickhouse_init_cid=$("${opik_compose[@]}" ps -aq clickhouse-init)
"${opik_compose[@]}" up -d --no-build --no-deps clickhouse
wait_container_healthy clickhouse 5
opik_redis_cid=$("${opik_compose[@]}" ps -q redis)
network_ready=0
for ((i=1; i<=120; i++)); do
  if docker exec "$opik_redis_cid" sh -c 'nc -z clickhouse 8123' >/dev/null 2>&1; then
    ((network_ready+=1))
    (( network_ready >= 10 )) && break
  else
    network_ready=0
  fi
  sleep 1
done
(( network_ready >= 10 )) || fail 'ClickHouse was not reachable from the Opik service network'
"${opik_compose[@]}" up -d --no-build mc
wait_container_success mc
"${opik_compose[@]}" up -d --no-build --no-deps backend
[[ $("${opik_compose[@]}" ps -aq clickhouse-init) == "$clickhouse_init_cid" ]] || \
  fail 'Opik clickhouse-init container was unexpectedly recreated'
otel_cid=$(docker compose -p "$project" -f "$work_dir/compose.yml" ps -q otel)
docker network connect "${project}-opik_default" "$otel_cid"
otel_mapping=$(docker compose -p "$project" -f "$work_dir/compose.yml" port otel 4317)
[[ "$otel_mapping" =~ ^127\.0\.0\.1:([0-9]+)$ ]] || fail "invalid Collector OTLP mapping: $otel_mapping"
otel_port=${BASH_REMATCH[1]}
otel_metrics_mapping=$(docker compose -p "$project" -f "$work_dir/compose.yml" port otel 8888)
[[ "$otel_metrics_mapping" =~ ^127\.0\.0\.1:([0-9]+)$ ]] || \
  fail "invalid Collector metrics mapping: $otel_metrics_mapping"
otel_metrics_port=${BASH_REMATCH[1]}
export E2E_OTEL_METRICS_PORT="$otel_metrics_port"
export OPIK_URL=${E2E_OPIK_URL:-http://127.0.0.1:${E2E_OPIK_BACKEND_PORT}}
export OPIK_OTLP_ENDPOINT=${E2E_OPIK_OTLP_ENDPOINT:-http://127.0.0.1:${E2E_OPIK_BACKEND_PORT}/v1/private/otel}
export OPIK_PROJECT='Default Project'
poll_opik

docker compose -p "$project" -f "$work_dir/compose.yml" up -d --wait milvus
milvus_port=$(docker compose -p "$project" -f "$work_dir/compose.yml" port milvus 19530 | awk -F: '{print $NF}')
milvus_proxy_port=$(python3 -c \
  'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
milvus_proxy_control_port=$(python3 -c \
  'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
export E2E_MILVUS_PROXY_ADDRESS="127.0.0.1:${milvus_proxy_port}"
export E2E_MILVUS_PROXY_TARGET="127.0.0.1:${milvus_port}"
export E2E_MILVUS_PROXY_CONTROL_ADDRESS="127.0.0.1:${milvus_proxy_control_port}"
export E2E_MILVUS_PROXY_URL="http://${E2E_MILVUS_PROXY_CONTROL_ADDRESS}"
setsid go run "$repo_dir/e2e/evaluation-evolution/tcp-proxy.go" >"$work_dir/milvus-proxy.log" 2>&1 &
milvus_proxy_pid=$!
poll 'Milvus TCP proxy' "curl -fsS '$E2E_MILVUS_PROXY_URL/state'"
export MILVUS_HOST=127.0.0.1 MILVUS_PORT="$milvus_proxy_port"
export E2E_MILVUS_PORT="$milvus_proxy_port"
export E2E_MILVUS_PUBLISHED_PORT="$milvus_port"
export E2E_MILVUS_CONTAINER
E2E_MILVUS_CONTAINER=$(docker compose -p "$project" -f "$work_dir/compose.yml" ps -q milvus)
[[ -n "$E2E_MILVUS_CONTAINER" ]] || fail 'isolated Milvus container ID is unavailable'

openssl genrsa -out "$work_dir/jwt.pem" 2048 >/dev/null 2>&1
export JWT_PRIVATE_KEY_PEM
JWT_PRIVATE_KEY_PEM=$(<"$work_dir/jwt.pem")
export GITHUB_CLIENT_ID=stratum-e2e-auth-wiring
export GITHUB_CLIENT_SECRET=''
[[ -n "$GITHUB_CLIENT_ID" && -n "$JWT_PRIVATE_KEY_PEM" ]] || \
  fail 'auth wiring requires both GITHUB_CLIENT_ID and JWT_PRIVATE_KEY_PEM'
export FRONTEND_URL=http://127.0.0.1:15173 PORT=18080 SECURE_COOKIES=false
export OTEL_EXPORTER_OTLP_ENDPOINT="127.0.0.1:${otel_port}"
printf 'collector-server-config: compose-mapping=%s allocated-port=%s endpoint=%s\n' \
  "$(docker compose -p "$project" -f "$work_dir/compose.yml" port otel 4317)" \
  "$otel_port" "$OTEL_EXPORTER_OTLP_ENDPOINT"
export TRACE_PAYLOAD_ENABLED=true TRACE_PAYLOAD_ENDPOINT="127.0.0.1:${minio_port}"
export TRACE_PAYLOAD_ACCESS_KEY=e2e-minio TRACE_PAYLOAD_SECRET_KEY=e2e-minio-secret
export TRACE_PAYLOAD_BUCKET="stratum-${run_id}" TRACE_PAYLOAD_USE_TLS=false
export E2E_MINIO_ENDPOINT="127.0.0.1:${minio_port}"

mcp_port=$(python3 -c \
  'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
export E2E_MCP_ADDRESS="127.0.0.1:${mcp_port}"
export E2E_MCP_URL="http://${E2E_MCP_ADDRESS}"
export E2E_MCP_EVIDENCE="$work_dir/mcp-evidence.txt"
setsid go run "$repo_dir/e2e/evaluation-evolution/mcp-server.go" >"$work_dir/mcp.log" 2>&1 & mcp_pid=$!
poll 'test MCP server' "curl -fsS -X POST -H 'Content-Type: application/json' \
  --data '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}' '$E2E_MCP_URL'"

llm_port=$(python3 -c \
  'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
export E2E_LLM_ADDRESS="127.0.0.1:${llm_port}"
export E2E_LLM_URL="http://${E2E_LLM_ADDRESS}"
export E2E_LLM_EVIDENCE="$work_dir/llm-evidence.txt"
export E2E_EMBED_EVIDENCE="$work_dir/embed-evidence.txt"
export QWEN_BASE_URL="$E2E_LLM_URL"
setsid go run "$repo_dir/e2e/evaluation-evolution/llm-server.go" >"$work_dir/llm.log" 2>&1 & llm_pid=$!
poll 'test LLM provider' "curl -fsS -X POST -H 'Content-Type: application/json' \
  --data '{\"failure\":false}' '$E2E_LLM_URL/mode'"

setsid bash -c 'cd "$1" && exec go run ./cmd/server' _ "$repo_dir" >"$backend_log" 2>&1 & backend_pid=$!
poll backend "curl -fsS http://127.0.0.1:18080/health"
readiness_verified=false
for ((i=1; i<=30; i++)); do
  if curl -fsS http://127.0.0.1:18080/readyz >/dev/null 2>&1; then
    readiness_verified=true
    break
  fi
  sleep 1
done
if [[ "$readiness_verified" != true ]]; then
  printf 'evaluation-evolution E2E concern: /readyz remained unavailable; continuing with /health evidence\n' >&2
fi
kill -0 "$backend_pid" 2>/dev/null || fail 'isolated backend exited after readiness poll'

# The fixture bootstrap intentionally uses the real guest and refresh flows. A generated
# tenant is then populated by the repository-owned bootstrap helper; no production tenant is queried.
export E2E_API_URL=http://127.0.0.1:18080 E2E_WEB_URL=http://127.0.0.1:15173 E2E_REPO_DIR="$repo_dir"
if ! go run "$repo_dir/e2e/evaluation-evolution/bootstrap.go" "$work_dir" "$run_id"; then
	collector_diagnostics
  python3 - "$backend_log" <<'PY'
import os, re, sys
path = sys.argv[1]
secret = os.environ.get('OPENAI_API_KEY', '')
lines = open(path, encoding='utf-8', errors='replace').read().splitlines()[-80:]
for line in lines:
    lower = line.lower()
    if any(marker in lower for marker in ('raw payload', 'upstream body', 'upstream response')):
        continue
    if secret:
        line = line.replace(secret, '[REDACTED]')
    line = re.sub(r'Bearer\s+[A-Za-z0-9._~+/=-]+', 'Bearer [REDACTED]', line, flags=re.I)
    line = re.sub(r'(?i)(api[_-]?key|access[_-]?token|credential|secret)([=: ]+)[^ ,;}]+', r'\1\2[REDACTED]', line)
    print(line)
PY
  fail 'isolated fixture bootstrap failed'
fi
assert_collector_binding
source "$work_dir/fixture.env"

export VITE_API_BASE_URL="$E2E_API_URL" VITE_PORT=15173
setsid bash -c 'cd "$1/web" && exec npm run dev -- --host 127.0.0.1' _ "$repo_dir" \
  >"$frontend_log" 2>&1 & frontend_pid=$!
poll frontend "curl -fsS http://127.0.0.1:15173/evaluations"
kill -0 "$frontend_pid" 2>/dev/null || fail 'isolated frontend exited after readiness poll'

(cd "$repo_dir" && npx --prefix web playwright test --config=e2e/evaluation-evolution/playwright.config.ts)
assert_collector_binding

scan_file="$work_dir/combined.log"
sed -E 's/Bearer [A-Za-z0-9._~+\/-]+/[REDACTED]/g' "$backend_log" >"$scan_file"
cat "$frontend_log" >>"$scan_file"
if rg -n -i 'Bearer [A-Za-z0-9._~+/-]+|raw[ _-]?payload|upstream[ _-]?(body|response)' "$scan_file"; then
  fail 'redaction scanner found forbidden output'
fi
if [[ -n "${OPENAI_API_KEY:-}" ]] && grep -Fq -- "$OPENAI_API_KEY" "$scan_file"; then
  fail 'redaction scanner found the known test secret'
fi

if ! cleanup_resources; then
  trap - EXIT INT TERM
  fail 'isolated E2E cleanup left project resources behind'
fi
trap - EXIT INT TERM
printf 'evaluation-evolution E2E passed: run=%s opik=%s\n' "$run_id" "$E2E_OPIK_VERSION"
