#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
run_dir=$(mktemp -d "${TMPDIR:-/tmp}/stratum-platform-assistant-browser.XXXXXX")
project="stratum-assistant-browser-$(basename "$run_dir" | tr -cd '[:alnum:]' | tr '[:upper:]' '[:lower:]')"
compose_file="$run_dir/compose.yml"
backend_log="$run_dir/backend.log"
frontend_log="$run_dir/frontend.log"
stub_log="$run_dir/stub.log"
backend_pid=''
frontend_pid=''
stub_pid=''

fail() {
  printf 'platform assistant browser E2E: %s\n' "$1" >&2
  exit 1
}

stop_process_group() {
  local pid=$1 label=$2
  [[ -n "$pid" ]] || return 0
  kill -TERM -- "-$pid" 2>/dev/null || true
  for _ in $(seq 1 50); do
    if ! kill -0 -- "-$pid" 2>/dev/null; then
      wait "$pid" 2>/dev/null || true
      return 0
    fi
    sleep 0.1
  done
  printf 'platform assistant browser E2E: force-stopping %s\n' "$label" >&2
  kill -KILL -- "-$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  set +e
  stop_process_group "$frontend_pid" frontend
  stop_process_group "$backend_pid" backend
  stop_process_group "$stub_pid" stub
  timeout 90 docker compose -p "$project" -f "$compose_file" down -v --remove-orphans >/dev/null 2>&1
  compose_status=$?
  rm -rf -- "$run_dir"
  if (( status == 0 && compose_status != 0 )); then
    status=$compose_status
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

poll() {
  local label=$1 command=$2
  for _ in $(seq 1 90); do
    if eval "$command" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  fail "$label did not become ready"
}

free_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'
}

for command in docker go npm npx openssl curl python3 setsid; do
  command -v "$command" >/dev/null 2>&1 || fail "$command is required"
done
docker info >/dev/null 2>&1 || fail 'Docker daemon is unavailable'

cat >"$compose_file" <<'YAML'
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: stratum
      POSTGRES_PASSWORD: stratum
      POSTGRES_DB: stratum
    ports: ["127.0.0.1::5432"]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U stratum -d stratum"]
      interval: 2s
      timeout: 2s
      retries: 30
  redis:
    image: redis:7.2-alpine
    ports: ["127.0.0.1::6379"]
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 2s
      timeout: 2s
      retries: 30
  nats:
    image: nats:2.10.25-alpine
    command: ["-js", "-m", "8222"]
    ports: ["127.0.0.1::4222"]
    healthcheck:
      test: ["CMD", "wget", "-q", "-O", "-", "http://127.0.0.1:8222/healthz"]
      interval: 2s
      timeout: 2s
      retries: 30
  etcd:
    image: quay.io/coreos/etcd:v3.5.16
    command: ["etcd", "-advertise-client-urls=http://127.0.0.1:2379", "-listen-client-urls=http://0.0.0.0:2379"]
  minio:
    image: minio/minio:RELEASE.2024-06-13T22-53-53Z
    environment:
      MINIO_ROOT_USER: minioadmin
      MINIO_ROOT_PASSWORD: minioadmin
    command: ["server", "/data"]
  milvus:
    image: milvusdb/milvus:v2.4.15
    command: ["milvus", "run", "standalone"]
    environment:
      ETCD_ENDPOINTS: etcd:2379
      MINIO_ADDRESS: minio:9000
    depends_on: [etcd, minio]
    ports: ["127.0.0.1::19530"]
YAML

docker compose -p "$project" -f "$compose_file" up -d --wait
postgres_port=$(docker compose -p "$project" -f "$compose_file" port postgres 5432 | awk -F: '{print $NF}')
redis_port=$(docker compose -p "$project" -f "$compose_file" port redis 6379 | awk -F: '{print $NF}')
nats_port=$(docker compose -p "$project" -f "$compose_file" port nats 4222 | awk -F: '{print $NF}')
milvus_port=$(docker compose -p "$project" -f "$compose_file" port milvus 19530 | awk -F: '{print $NF}')
for port in "$postgres_port" "$redis_port" "$nats_port" "$milvus_port"; do
  [[ "$port" =~ ^[0-9]+$ ]] || fail 'Docker did not assign all dependency ports'
done

stub_port=$(free_port)
backend_port=$(free_port)
frontend_port=$(free_port)
stub_url="http://127.0.0.1:${stub_port}"
backend_url="http://127.0.0.1:${backend_port}"
frontend_url="http://127.0.0.1:${frontend_port}"

setsid go run ./test/e2e/cmd/platform-assistant-stubs \
  -listen-address "127.0.0.1:${stub_port}" \
  -expected-tool stratum_propose_resource_change >"$stub_log" 2>&1 &
stub_pid=$!
poll 'platform-assistant-stubs /readyz' "curl -fsS '$stub_url/readyz'"

openssl genrsa -out "$run_dir/jwt.pem" 2048 >/dev/null 2>&1
export JWT_PRIVATE_KEY_PEM
JWT_PRIVATE_KEY_PEM=$(<"$run_dir/jwt.pem")
export PORT="$backend_port"
export FRONTEND_URL="$frontend_url"
export SECURE_COOKIES=false
export GITHUB_CLIENT_ID=platform-assistant-browser-e2e
export GITHUB_CLIENT_SECRET=''
export POSTGRES_URL="postgres://stratum:stratum@127.0.0.1:${postgres_port}/stratum?sslmode=disable"
export REDIS_URL="redis://127.0.0.1:${redis_port}"
export NATS_URL="nats://127.0.0.1:${nats_port}"
export MILVUS_HOST=127.0.0.1
export MILVUS_PORT="$milvus_port"
export QWEN_BASE_URL="$stub_url/v1"
export OPIK_URL=''

setsid go run ./cmd/server >"$backend_log" 2>&1 &
backend_pid=$!
poll 'backend /readyz' "curl -fsS '$backend_url/readyz'"
kill -0 "$backend_pid" 2>/dev/null || fail 'backend exited after readiness'

setsid env VITE_API_BASE_URL="$backend_url" VITE_PORT="$frontend_port" CI=1 \
  npm --prefix web run dev -- --host 127.0.0.1 --port "$frontend_port" >"$frontend_log" 2>&1 &
frontend_pid=$!
poll 'frontend' "curl -fsS '$frontend_url'"

export REAL_PLATFORM_ASSISTANT_E2E=1
export E2E_API_URL="$backend_url"
export E2E_WEB_URL="$frontend_url"
export E2E_POSTGRES_CONTAINER
E2E_POSTGRES_CONTAINER=$(docker compose -p "$project" -f "$compose_file" ps -q postgres)
[[ -n "$E2E_POSTGRES_CONTAINER" ]] || fail 'PostgreSQL container ID is unavailable'

cd web
npx playwright test --config playwright.real.config.ts
