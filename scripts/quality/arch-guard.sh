#!/usr/bin/env bash
# 架构守卫（单一真相源）：检测 api/wiring 层的越界裸 SQL。
#
# 契约：$@ = 一个或多个待检 Go 文件路径（绝对或相对）。
#   任一文件命中越界 → stdout 汇总人类可读违规报告，exit 1
#   全部合规 / 不适用 → 静默，exit 0
#
# 多文件契约兼容两类调用方：
#   • pre-commit 框架：默认按 types 过滤后一次性传入多个暂存文件名
#   • Claude/Codex PostToolUse hook：单文件（$1）
#
# 为什么只抓 wiring 裸 SQL：
#   wiring 合法 import infrastructure（构造 infra 实例是其职责），故 import 方向由
#   depguard 管，本守卫不重复。wiring 的越界信号是“散写 SQL/编排”——tx/conn 直接
#   跑 SQL 即违背“wiring 只做薄 ACL 转发，SQL 归 infrastructure”。
#   业务规则（如 patch 白名单、FK 逻辑）语义不可判定，本守卫不涉及，靠 review + 测试。

set -euo pipefail

RC=0
REPORT=""

for FILE in "$@"; do
  [[ -n "$FILE" && -f "$FILE" ]] || continue
  [[ "$FILE" == *.go ]] || continue
  [[ "$FILE" != *_test.go ]] || continue

  # 仅对 api/wiring/ 下的文件生效
  case "$FILE" in
    */api/wiring/*.go | api/wiring/*.go) ;;
    *) continue ;;
  esac

  # 越界信号：wiring 内 tx/conn 直接执行 SQL（含事务回调内的裸调用）
  HITS="$(grep -nE '\b(tx|conn)\.(QueryRow|Query|Exec|SendBatch|CopyFrom)\(' "$FILE" || true)"
  [[ -z "$HITS" ]] && continue

  RC=1
  REPORT+="⛔ 架构越界（api/wiring 禁散写 SQL）：${FILE}
${HITS}

"
done

[[ "$RC" -eq 0 ]] && exit 0

printf '%s' "$REPORT"
cat <<'EOF'
wiring 职责 = 构造 app+infra、装配 Container、逆序 Shutdown、跨 ctx ACL（薄转发）。
裸 SQL 属 infrastructure：
  • 表访问 / SQL      → 移至 internal/<ctx>/infrastructure 的 repository
  • 编排 / 事务        → 移至 internal/<ctx>/application 的 service
  • wiring 的 adapter → 只保留接口形状转换，委托已有 application 方法
    （参考同层 skillCandidateManager / gatewayPromptRewriter 的薄转发写法）
EOF
exit 1
