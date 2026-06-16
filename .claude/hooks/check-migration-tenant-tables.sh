#!/usr/bin/env bash
# Hook: 拦截向编号迁移文件写入 tenant-only 表的 DDL
# 触发：Write|Edit，路径匹配 internal/migration/sql/[0-9]*.sql

set -euo pipefail

INPUT=$(cat)

FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // ""')

# 只检查编号迁移 up/down 文件
if ! echo "$FILE_PATH" | grep -qE 'internal/migration/sql/[0-9]+.*\.sql$'; then
  echo '{"continue": true}'
  exit 0
fi

PROJ_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
TENANT_SCHEMA="$PROJ_ROOT/pkg/tenantdb/tenant_schema.sql"

if [[ ! -f "$TENANT_SCHEMA" ]]; then
  echo '{"continue": true}'
  exit 0
fi

# 动态提取所有 tenant 表名
TENANT_TABLES=$(grep -oE 'CREATE TABLE (IF NOT EXISTS )?[a-z_]+' "$TENANT_SCHEMA" \
  | awk '{print $NF}' | sort -u)

# 获取即将写入的 SQL 内容
CONTENT=$(echo "$INPUT" | jq -r '.tool_input.content // .tool_input.new_string // ""')

# 逐表检查
for TABLE in $TENANT_TABLES; do
  if echo "$CONTENT" | grep -qiE "(ALTER|CREATE|DROP|INSERT INTO|UPDATE|DELETE FROM)[[:space:]]+(TABLE[[:space:]]+)?(IF[[:space:]]+EXISTS[[:space:]]+)?${TABLE}[[:space:](]"; then
    PUBLIC_TABLES="users, tenants, tenant_members, invitations, refresh_tokens, tenant_api_keys, audit_logs, model_providers, models"
    MSG="🚫 迁移规则违反: '${TABLE}' 是 tenant-schema 表，不能出现在编号迁移文件中。\n\n编号迁移 (internal/migration/sql/NNN_*.sql) 只能操作 public schema 表:\n  ${PUBLIC_TABLES}\n\n正确做法: 将此 DDL 放入 pkg/tenantdb/tenant_schema.sql"
    echo "{\"continue\": false, \"stopReason\": \"$(echo -e "$MSG" | sed 's/"/\\"/g' | tr '\n' '|')\"}"
    exit 0
  fi
done

echo '{"continue": true}'
