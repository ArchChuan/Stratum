#!/usr/bin/env bash
# 迁移脚本回归测试：验证计数用 CTE 包 UPDATE、外层 SELECT count(*) 精确计数
# （修复 RETURNING id | wc -l 恒多计 1 的缺陷：0 行被误报为 1、1 行被报 2），
# 且脚本幂等（二次运行 0 影响）。
#
# 需要真实 postgres + psql CLI：DATABASE_URL 必填；测试用 superuser 建独立
# scratch 库，隔离共享库中其他租户（缺 rag_workspaces 表会令脚本 fail-fast
# 中止，属脚本既有行为）的干扰。
# 本地：make infra-up 后
#   DATABASE_URL=postgres://stratum:stratum@localhost:5432/stratum?sslmode=disable \
#     bash scripts/knowledge-rerank-workspaces-migration-test.sh
# 说明：本测试不挂在默认 CI——CI 的 migration job 无 psql CLI、共享库有 go-test
# 租户残留。作为手动门禁目标 migration-script-guardrails（镜像
# migration-db-guardrails 需真实 PG 的先例）。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MIGRATION="${SCRIPT_DIR}/knowledge-rerank-workspaces-migration.sh"

: "${DATABASE_URL:?DATABASE_URL is required}"

# 独立 scratch 库（stratum 为官方 postgres 镜像超管，可 CREATE/DROP DATABASE）
SCRATCH_DB="stratum_rerank_migration_ci"
SCRATCH_ID="00000000-0000-0000-0000-00000000d1ce"
SCRATCH_SCHEMA="tenant_${SCRATCH_ID}"

# 取 DATABASE_URL 的 host:port 部分建独立库（% 切最短后缀，去 db 名与查询串；
# %% 会从第一个 / 切出 "postgres:"，连接串就废了）
DB_BASE="${DATABASE_URL%/*}"
PSQL_ROOT="psql -v ON_ERROR_STOP=1 ${DB_BASE}/postgres?sslmode=disable"
TEST_URL="${DB_BASE}/${SCRATCH_DB}?sslmode=disable"

# shellcheck disable=SC2086
${PSQL_ROOT} -c "DROP DATABASE IF EXISTS ${SCRATCH_DB};" >/dev/null
# shellcheck disable=SC2086
${PSQL_ROOT} -c "CREATE DATABASE ${SCRATCH_DB};" >/dev/null

cleanup() {
  # shellcheck disable=SC2086
  ${PSQL_ROOT} -c "DROP DATABASE IF EXISTS ${SCRATCH_DB};" >/dev/null 2>&1 || true
}
trap cleanup EXIT

runpsql() { psql -v ON_ERROR_STOP=1 -tAc "$1" "${TEST_URL}"; }

# 建表 + 种子租户 + 三行受控 workspace：
#   A. builtin-score-v1 无 rerank_model → 应被清（count 1）
#   B. builtin-score-v1 有 rerank_model  → 不应被清
#   C. 无 reranking 键                   → 不应被清
runpsql "CREATE TABLE public.tenants (id UUID PRIMARY KEY, name TEXT NOT NULL, slug TEXT NOT NULL UNIQUE, deleted_at TIMESTAMPTZ);" >/dev/null
runpsql "INSERT INTO public.tenants (id, name, slug) VALUES ('${SCRATCH_ID}', 'tmp-rerank-ci', 'tmp-rerank-ci-${SCRATCH_ID}');" >/dev/null
runpsql "CREATE SCHEMA \"${SCRATCH_SCHEMA}\";" >/dev/null
runpsql "CREATE TABLE \"${SCRATCH_SCHEMA}\".rag_workspaces (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), config JSONB NOT NULL DEFAULT '{}');" >/dev/null
runpsql "INSERT INTO \"${SCRATCH_SCHEMA}\".rag_workspaces (config) VALUES ('{\"reranking\":\"builtin-score-v1\"}');" >/dev/null
runpsql "INSERT INTO \"${SCRATCH_SCHEMA}\".rag_workspaces (config) VALUES ('{\"reranking\":\"builtin-score-v1\",\"rerank_model\":\"qwen-turbo\"}');" >/dev/null
runpsql "INSERT INTO \"${SCRATCH_SCHEMA}\".rag_workspaces (config) VALUES ('{\"top_k\":5}');" >/dev/null

# 首跑：只应清 1 个（旧实现误报 2：1 个 uuid + "UPDATE 1" 命令标签）
out=$(DATABASE_URL="${TEST_URL}" bash "${MIGRATION}")
printf '%s\n' "${out}"
grep -F "${SCRATCH_SCHEMA}: reranking cleared for 1 workspace(s)" <<<"${out}" >/dev/null
grep -F "migration done: 1 workspace(s) reranking cleared across 1 tenant schema(s)" <<<"${out}" >/dev/null

# 终态：A 清空（reranking 键存在且为空），B/C 不变
[ "$(runpsql "SELECT count(*) FROM \"${SCRATCH_SCHEMA}\".rag_workspaces WHERE config->>'reranking' = ''")" = "1" ]
[ "$(runpsql "SELECT count(*) FROM \"${SCRATCH_SCHEMA}\".rag_workspaces WHERE config->>'rerank_model' = 'qwen-turbo'")" = "1" ]
[ "$(runpsql "SELECT count(*) FROM \"${SCRATCH_SCHEMA}\".rag_workspaces WHERE NOT (config ? 'reranking')")" = "1" ]

# 幂等：二次运行 0 影响
out2=$(DATABASE_URL="${TEST_URL}" bash "${MIGRATION}")
printf '%s\n' "${out2}"
grep -F "migration done: 0 workspace(s) reranking cleared across 0 tenant schema(s)" <<<"${out2}" >/dev/null

echo "knowledge-rerank-workspaces-migration test passed"
