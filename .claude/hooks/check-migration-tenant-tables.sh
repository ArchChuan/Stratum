#!/usr/bin/env bash
# Hook(PreToolUse:Write|Edit)：拦截向编号迁移文件写入 tenant-only 表的 DDL。
# 触发路径：internal/migration/sql/NNN_*.sql
# 规则(CLAUDE.md 红线)：编号迁移只能操作 public schema 表；tenant-only 表的 DDL
# 必须放入 pkg/storage/postgres/tenant_schema.sql，由 ProvisionAllTenantSchemas 执行。

set -euo pipefail
source "$(dirname "$0")/lib.sh"

hook_read_input
FILE_PATH="$(hook_target_path)"

# 只检查编号迁移 up/down 文件，其余放行
if ! printf '%s' "$FILE_PATH" | grep -qE 'internal/migration/sql/[0-9]+.*\.sql$'; then
  pre_allow
fi

PROJ_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
TENANT_SCHEMA="$PROJ_ROOT/pkg/storage/postgres/tenant_schema.sql"

# schema 文件缺失属于配置错误：硬失败而非静默放行（护栏假在场比没有更危险）
if [[ ! -f "$TENANT_SCHEMA" ]]; then
  pre_deny "🚫 迁移校验 hook 配置错误: 找不到 tenant_schema.sql (期望位于 pkg/storage/postgres/)，护栏无法工作，请修复 hook 脚本路径。"
fi

# 动态提取所有 tenant 表名（single source：从 schema 文件解析，schema 变更自动跟随）
TENANT_TABLES=$(grep -oE 'CREATE TABLE (IF NOT EXISTS )?[a-z_]+' "$TENANT_SCHEMA" \
  | awk '{print $NF}' | sort -u)

CONTENT="$(hook_target_content)"

PUBLIC_TABLES="users, tenants, tenant_members, invitations, refresh_tokens, tenant_api_keys, audit_logs, model_providers, models"

for TABLE in $TENANT_TABLES; do
  if printf '%s' "$CONTENT" | grep -qiE "(ALTER|CREATE|DROP|INSERT INTO|UPDATE|DELETE FROM)[[:space:]]+(TABLE[[:space:]]+)?(IF[[:space:]]+EXISTS[[:space:]]+)?${TABLE}[[:space:](]"; then
    pre_deny "🚫 迁移规则违反: '${TABLE}' 是 tenant-schema 表，不能出现在编号迁移文件中。编号迁移 (internal/migration/sql/NNN_*.sql) 只能操作 public schema 表: ${PUBLIC_TABLES}。正确做法: 将此 DDL 放入 pkg/storage/postgres/tenant_schema.sql。"
  fi
done

pre_allow
