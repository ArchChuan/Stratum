#!/usr/bin/env bash
# Hook(PreToolUse:Bash)：拦截高破坏力命令。
# deny 只否决当前调用并回喂原因，模型可改用更安全的等价命令。

set -euo pipefail
source "$(dirname "$0")/lib.sh"

hook_read_input
CMD="$(hook_field '.tool_input.command')"

# 破坏面：递归删除 / 提权 / 全权限 / 改属主 / 裸写块设备 / 建文件系统 / 分区
DANGER_RE='(^|[;&|]|[[:space:]])(rm[[:space:]]+-rf|sudo|su[[:space:]]|chmod[[:space:]]+777|chown[[:space:]]|dd[[:space:]]+if=|mkfs|fdisk)([[:space:]]|$)'

if printf '%s' "$CMD" | grep -qE "$DANGER_RE"; then
  pre_deny "🚫 禁止命令: 该命令可能造成不可逆数据丢失或提权风险。若确需执行，请缩小作用范围（指定具体路径）或改用非破坏性等价命令。"
fi

pre_allow
