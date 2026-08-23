#!/usr/bin/env bash
# 迁移存量 workspace：builtin-score-v1 且缺 rerank_model 的配置，将 reranking 置空，
# 使 #4.2 校验（builtin 空模型保存拒绝）上线后不受影响。
# rag_workspaces 是 tenant-only 表（tenant_schema.sql），编号迁移只操作 public
# schema，故按 public.tenants 枚举逐 tenant_<id> schema 幂等执行。
# schema 名形如 tenant_<uuid>（含连字符），PostgreSQL 中未加引号的标识符
# 不允许 '-',必须用 "..." 包裹，否则语法错误（syntax error at or near "-"）。
# 用法：DATABASE_URL=postgres://... bash scripts/knowledge-rerank-workspaces-migration.sh
set -euo pipefail

: "${DATABASE_URL:?DATABASE_URL is required}"

enabled=0
affected=0
# 枚举全部 tenant（含历史租户）。单独赋值让 set -e 能捕获枚举失败，避免误报 0 行成功。
schemas=$(psql "$DATABASE_URL" -tAc "SELECT 'tenant_'||id FROM public.tenants WHERE deleted_at IS NULL")
for schema in $schemas; do
  # psql -tA 只抑制表头/页脚，不抑制命令标签：UPDATE...RETURNING 即使 0 行
  # 受影响也会向 stdout 打 "UPDATE N"，RETURNING id | wc -l 会恒多计 1
  # （0 行被误报为 1、1 行被报 2，生产首跑曾把 5 个 0 行租户误报为 cleared 5）。
  # 用 CTE 包 UPDATE、外层 SELECT count(*) 精确计数：0/1/N 行分别输出 0/1/N。
  n=$(psql "$DATABASE_URL" -tAc \
    "WITH changed AS (
       UPDATE \"${schema}\".rag_workspaces SET config = config || '{\"reranking\":\"\"}'
       WHERE config->>'reranking' = 'builtin-score-v1'
         AND (config->>'rerank_model' IS NULL OR config->>'rerank_model' = '')
       RETURNING id
     ) SELECT count(*) FROM changed")
  n=${n//[[:space:]]/}
  if [ "$n" -gt 0 ]; then
    affected=$((affected + n))
    enabled=$((enabled + 1))
    echo "${schema}: reranking cleared for ${n} workspace(s)"
  fi
done
echo "migration done: ${affected} workspace(s) reranking cleared across ${enabled} tenant schema(s)"
