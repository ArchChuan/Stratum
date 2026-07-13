#!/usr/bin/env bash
# Hook(PreToolUse:Read|Write|Edit)：文件系统访问保护。
# 敏感凭据文件：读写都拦（防泄露）。
# 系统关键目录 / 生产配置：仅拦写（读通常无害，且需要排查系统状态）。

set -euo pipefail
source "$(dirname "$0")/lib.sh"

hook_read_input
FILE_PATH="$(hook_target_path)"
TOOL="$(hook_tool_name)"

# 空路径直接放行（交给下游 hook / 工具本身处理）
[[ -z "$FILE_PATH" ]] && pre_allow

# 是否为写类操作（Read 之外都按写处理，含 Write/Edit/MultiEdit）
is_write() { [[ "$TOOL" != "Read" ]]; }

# 1) 敏感凭据文件：读写都拦（防止密钥/凭据外泄）
if printf '%s' "$FILE_PATH" | grep -qE '(/\.ssh/|/\.aws/|/\.kube/|/etc/shadow|/etc/passwd|/\.aws/credentials)'; then
  pre_deny "🚫 敏感文件: 禁止访问凭据/密钥文件 (${FILE_PATH})。"
fi

# 以下仅对写类操作生效
if is_write; then
  # 2) 系统关键目录
  if printf '%s' "$FILE_PATH" | grep -qE '^(/etc|/sys|/proc|/dev|/root|/var/log|/boot|/usr/bin|/usr/sbin)(/|$)'; then
    pre_deny "🚫 危险目录: 禁止写入系统关键目录 (${FILE_PATH})。"
  fi

  # 3) 生产配置（CLAUDE.md 红线：禁止修改 config/prod.yaml）
  if printf '%s' "$FILE_PATH" | grep -qE '(^|/)config/prod\.yaml$'; then
    pre_deny "🚫 生产配置: config/prod.yaml 禁止修改（CLAUDE.md 红线）。如需调整生产参数，走部署流程或环境变量注入。"
  fi
fi

pre_allow
