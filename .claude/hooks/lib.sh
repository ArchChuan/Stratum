#!/usr/bin/env bash
# 公共库：所有 hook 共享的 stdin 解析与 JSON 输出原语。
# 单一真相源，禁止在各 hook 内联重复这些逻辑。
#
# Claude Code hook 输出协议（PreToolUse）：
#   - 放行： {"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}
#   - 拒绝： {"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"..."}}
#   deny 只否决“当前这一次工具调用”，并把 reason 回喂模型，让它换安全做法。
#   注意：不要用顶层 {"continue":false} —— 那会中止整个 turn，语义过重。
#
# PostToolUse 反馈协议：
#   - 非阻塞注入上下文（推荐）：{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"..."}}
#   - 阻塞并要求修复：{"decision":"block","reason":"..."}

set -euo pipefail

# 读取整个 stdin 到全局 HOOK_INPUT（一次性，避免多次 cat 阻塞）
hook_read_input() {
  HOOK_INPUT="$(cat)"
}

# 从 HOOK_INPUT 取字段：$1=jq 表达式，$2=默认值
hook_field() {
  local expr="$1" default="${2:-}"
  printf '%s' "$HOOK_INPUT" | jq -r "${expr} // \"${default}\""
}

# 被 Write|Edit|Read 操作的目标路径（兼容 file_path / filePath）
hook_target_path() {
  printf '%s' "$HOOK_INPUT" | jq -r '.tool_input.file_path // .tool_response.filePath // ""'
}

# 触发本次 hook 的工具名（Bash / Read / Write / Edit ...）
hook_tool_name() {
  printf '%s' "$HOOK_INPUT" | jq -r '.tool_name // ""'
}

# 即将写入的内容（兼容 content / new_string）
hook_target_content() {
  printf '%s' "$HOOK_INPUT" | jq -r '.tool_input.content // .tool_input.new_string // ""'
}

# PreToolUse 放行
pre_allow() {
  printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}\n'
  exit 0
}

# PreToolUse 拒绝：$1=面向模型的原因（会被安全地 JSON 编码）
pre_deny() {
  local reason="$1"
  jq -cn --arg r "$reason" \
    '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:$r}}'
  exit 0
}

# PostToolUse 非阻塞注入上下文：$1=文本
post_context() {
  local ctx="$1"
  jq -cn --arg c "$ctx" \
    '{hookSpecificOutput:{hookEventName:"PostToolUse",additionalContext:$c}}'
  exit 0
}
