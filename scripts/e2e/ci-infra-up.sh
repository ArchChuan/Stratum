#!/usr/bin/env bash
# CI infra startup for stateful E2E.
# Called via STATEFUL_E2E_INFRA_UP_COMMAND — must leave services running.
set -euo pipefail

project="stratum-stateful-ci"
# Reuse existing compose if already running (idempotent on retry).
compose_file="${TMPDIR:-/tmp}/stratum-stateful-ci-infra.yml"
export STATEFUL_E2E_CI_COMPOSE_FILE=$compose_file
export STATEFUL_E2E_CI_PROJECT=$project

if [[ -f "$compose_file" ]] && docker compose -p "$project" -f "$compose_file" ps --format '{{.ID}}' 2>/dev/null | grep -q .; then
  echo "CI infra already running, reusing"
  exit 0
fi

cat >"$compose_file" <<'YAML'
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: stratum
      POSTGRES_PASSWORD: stratum
      POSTGRES_DB: stratum
    ports: ["127.0.0.1:5432:5432"]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U stratum -d stratum"]
      interval: 2s
      timeout: 2s
      retries: 30
  redis:
    image: redis:7.2-alpine
    ports: ["127.0.0.1:6379:6379"]
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 2s
      timeout: 2s
      retries: 30
  nats:
    image: nats:2.10.25-alpine
    command: ["-js", "-m", "8222"]
    ports: ["127.0.0.1:4222:4222"]
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
    ports: ["127.0.0.1:19530:19530"]
YAML

docker compose -p "$project" -f "$compose_file" up -d --wait

export REDIS_URL="redis://127.0.0.1:6379"
export NATS_URL="nats://127.0.0.1:4222"
export MILVUS_HOST=127.0.0.1
export MILVUS_PORT=19530

echo "CI infra ready: postgres=5432 redis=6379 nats=4222 milvus=19530"
