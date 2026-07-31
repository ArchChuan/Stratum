#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$root"

make risk-guardrails code-quality

clean_env=(env -u STRATUM_TEST_POSTGRES_URL -u TEST_DATABASE_URL -u POSTGRES_URL
  -u REDIS_URL -u NATS_URL -u MILVUS_HOST -u MILVUS_PORT)
"${clean_env[@]}" go vet ./...
"${clean_env[@]}" go test -short ./...
"${clean_env[@]}" go test ./... -count=1
make contract-test fe-lint fe-build
