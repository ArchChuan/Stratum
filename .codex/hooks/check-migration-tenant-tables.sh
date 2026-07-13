#!/usr/bin/env bash
# 转发到 .claude/hooks 的规范实现，避免双份漂移（单一真相源）。
# 历史上此处有独立副本，出现过 stale fallback（pkg/tenantdb 错路径）+ 静默放行两个 bug；
# 现统一由 .claude/hooks/check-migration-tenant-tables.sh 负责，stdin 通过 exec 继承。
set -euo pipefail
exec bash "$(cd "$(dirname "$0")/../../.claude/hooks" && pwd)/check-migration-tenant-tables.sh"
