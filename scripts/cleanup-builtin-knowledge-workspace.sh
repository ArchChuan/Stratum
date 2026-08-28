#!/usr/bin/env bash
# 清理内置知识库(stratum_docs workspace)的存量 DB 残留。
# 背景:内置 workspace a0a0a0a0-0000-0000-0000-000000000001 已从种子 SQL 移除,
# 存量租户仍可能有该 workspace 及其文档/向量残留。本脚本逐非 deleted 租户幂等
# 清理 DB 侧残留;Milvus 共享 collection 由配套一次性工具
# cmd/cleanup-builtin-knowledge-milvus 处理(脚本结尾打印指引)。
# rag_workspaces / knowledge_docs 等是 tenant-only 表(tenant_schema.sql),编号迁移
# 只操作 public schema,故按 public.tenants 枚举逐 tenant_<id> schema 幂等执行。
# schema 名形如 tenant_<uuid>(含连字符),PostgreSQL 中未加引号的标识符不允许 '-',
# 必须用 "..." 包裹,否则语法错误(syntax error at or near "-")。
# 幂等说明:内置 workspace 已从种子移除,重复执行各 DELETE 命中 0 行,无害。
# 用法:
#   DATABASE_URL=postgres://... bash scripts/cleanup-builtin-knowledge-workspace.sh            # dry-run,只列计数
#   DATABASE_URL=postgres://... bash scripts/cleanup-builtin-knowledge-workspace.sh --execute  # 真删
set -euo pipefail

: "${DATABASE_URL:?DATABASE_URL is required}"

# 内置 workspace ID(a0a0a0a0-... 原 BuiltinWorkspaceID 常量,常量已随内置知识库移除,
# 此处硬编码为清理锚点;内置 ID 跨租户固定,不会与任何业务 workspace 冲突)。
BUILTIN_WS_ID="a0a0a0a0-0000-0000-0000-000000000001"

execute=0
case "${1:-}" in
  "") ;;
  --execute) execute=1 ;;
  *) echo "unknown argument: $1 (usage: [--execute])" >&2; exit 2 ;;
esac

# 枚举全部 tenant(含历史租户)。单独赋值让 set -e 能捕获枚举失败,避免误报 0 行成功。
schemas=$(psql "$DATABASE_URL" -tAc "SELECT 'tenant_'||id FROM public.tenants WHERE deleted_at IS NULL")

verb="would delete"
[ "$execute" -eq 1 ] && verb="deleted"

tenants_affected=0
tot_bindings=0
tot_docs=0
tot_chunks=0
tot_workspaces=0

for schema in $schemas; do
  # psql -tA 只抑制表头/页脚,不抑制命令标签:DELETE...RETURNING 即使 0 行受影响
  # 也会向 stdout 打 "DELETE N",RETURNING id | wc -l 会恒多计 1。
  # 用 CTE 包 DELETE、外层 SELECT count(*) 精确计数:0/1/N 行分别输出 0/1/N。
  # dry-run 时改跑 SELECT count(*) 只读计数,不产生任何写入。
  if [ "$execute" -eq 1 ]; then
    b=$(psql "$DATABASE_URL" -tAc "WITH del AS (DELETE FROM \"${schema}\".agent_workspaces WHERE workspace_id = '${BUILTIN_WS_ID}' RETURNING 1) SELECT count(*) FROM del")
    d=$(psql "$DATABASE_URL" -tAc "WITH del AS (DELETE FROM \"${schema}\".knowledge_docs WHERE workspace_id = '${BUILTIN_WS_ID}' RETURNING 1) SELECT count(*) FROM del")
    c=$(psql "$DATABASE_URL" -tAc "WITH del AS (DELETE FROM \"${schema}\".knowledge_chunks WHERE workspace_id = '${BUILTIN_WS_ID}' RETURNING 1) SELECT count(*) FROM del")
    w=$(psql "$DATABASE_URL" -tAc "WITH del AS (DELETE FROM \"${schema}\".rag_workspaces WHERE id = '${BUILTIN_WS_ID}' RETURNING 1) SELECT count(*) FROM del")
  else
    b=$(psql "$DATABASE_URL" -tAc "SELECT count(*) FROM \"${schema}\".agent_workspaces WHERE workspace_id = '${BUILTIN_WS_ID}'")
    d=$(psql "$DATABASE_URL" -tAc "SELECT count(*) FROM \"${schema}\".knowledge_docs WHERE workspace_id = '${BUILTIN_WS_ID}'")
    c=$(psql "$DATABASE_URL" -tAc "SELECT count(*) FROM \"${schema}\".knowledge_chunks WHERE workspace_id = '${BUILTIN_WS_ID}'")
    w=$(psql "$DATABASE_URL" -tAc "SELECT count(*) FROM \"${schema}\".rag_workspaces WHERE id = '${BUILTIN_WS_ID}'")
  fi
  b=${b//[[:space:]]/}
  d=${d//[[:space:]]/}
  c=${c//[[:space:]]/}
  w=${w//[[:space:]]/}
  total=$((b + d + c + w))
  if [ "$total" -gt 0 ]; then
    tenants_affected=$((tenants_affected + 1))
    tot_bindings=$((tot_bindings + b))
    tot_docs=$((tot_docs + d))
    tot_chunks=$((tot_chunks + c))
    tot_workspaces=$((tot_workspaces + w))
    echo "${schema}: ${verb} agent_workspaces=${b} knowledge_docs=${d} knowledge_chunks=${c} rag_workspaces=${w}"
  fi
done

echo "cleanup done: ${verb} ${tot_bindings} agent binding(s), ${tot_docs} doc(s), ${tot_chunks} chunk(s), ${tot_workspaces} workspace(s) across ${tenants_affected} tenant schema(s)"

if [ "$execute" -ne 1 ]; then
  echo "dry-run: no data was deleted. Re-run with --execute to apply."
  echo "Milvus 共享 collection 未处理:如存量租户曾写入向量,需再运行一次性工具"
  echo "  MILVUS_HOST=... MILVUS_PORT=... go run ./cmd/cleanup-builtin-knowledge-milvus [-execute]"
  echo "(该工具只枚举 kb_a0a0a0a0_0000_0000_0000_000000000001 前缀的 collection,不触碰其他数据)"
fi
