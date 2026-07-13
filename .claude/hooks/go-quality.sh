#!/usr/bin/env bash
# Hook(PostToolUse:Write|Edit)：Go 文件质量闭环。
# 1) gofmt -w 自动格式化（消除风格 diff，Go 工程通用规范）
# 2) go vet 包级静态检查，问题通过 additionalContext 回喂模型（可见，非静默）
# 成功则静默（不产生 additionalContext，避免噪音）。

set -euo pipefail
source "$(dirname "$0")/lib.sh"

hook_read_input
FILE_PATH="$(hook_target_path)"

# 只处理 Go 源码
[[ "$FILE_PATH" == *.go ]] || exit 0
[[ -f "$FILE_PATH" ]] || exit 0

PROJ_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
GOFMT="$(command -v gofmt || true)"
GO_BIN="$(command -v go || true)"

MSGS=()

# 1) gofmt：先探测是否有 diff，有才写回并汇报（透明）
if [[ -n "$GOFMT" ]] && ! "$GOFMT" -l "$FILE_PATH" | grep -q .; then
  : # 已格式化，无操作
elif [[ -n "$GOFMT" ]]; then
  "$GOFMT" -w "$FILE_PATH" 2>/dev/null || true
  MSGS+=("✓ gofmt 已自动格式化 ${FILE_PATH##*/}")
fi

# 2) go vet：包级，带超时（vet 可能拉全包，90s 硬顶）
#    仅对项目内文件执行；项目外文件（如临时文件）跳过 vet，避免路径畸形
ABS_PATH="$(cd "$(dirname "$FILE_PATH")" 2>/dev/null && pwd)/$(basename "$FILE_PATH")"
if [[ -n "$GO_BIN" && "$ABS_PATH" == "$PROJ_ROOT"/* ]]; then
  REL_DIR="$(dirname "${ABS_PATH#"$PROJ_ROOT"/}")"
  VET_OUT="$(cd "$PROJ_ROOT" && timeout 90 "$GO_BIN" vet "./$REL_DIR/..." 2>&1 || true)"
  if [[ -n "$VET_OUT" ]]; then
    MSGS+=("⚠️ go vet 报告问题（./$REL_DIR）："$'\n'"$VET_OUT")
  fi
fi

# 有反馈才注入上下文；否则静默退出
if [[ ${#MSGS[@]} -gt 0 ]]; then
  printf -v JOINED '%s\n' "${MSGS[@]}"
  post_context "$JOINED"
fi

exit 0
