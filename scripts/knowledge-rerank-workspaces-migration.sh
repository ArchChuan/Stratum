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
  # -tA 元组模式会吞掉 UPDATE 命令标签（UPDATE N），本身不输出行；用 RETURNING
  # 产出被改行并以 wc -l 计数，否则 affected 恒为 0。
  n=$(psql "$DATABASE_URL" -tAc \
    "UPDATE \"${schema}\".rag_workspaces SET config = config || '{\"reranking\":\"\"}' \
     WHERE config->>'reranking' = 'builtin-score-v1' \
       AND (config->>'rerank_model' IS NULL OR config->>'rerank_model' = '') RETURNING id" \
    | wc -l)
  n=${n//[[:space:]]/}
  if [ "$n" -gt 0 ]; then
    affected=$((affected + n))
    enabled=$((enabled + 1))
    echo "${schema}: reranking cleared for ${n} workspace(s)"
  fi
done
echo "migration done: ${affected} workspace(s) reranking cleared across ${enabled} tenant schema(s)"
