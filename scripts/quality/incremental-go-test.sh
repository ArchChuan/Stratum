#!/usr/bin/env bash
set -euo pipefail

# 增量 Go 测试：只运行 staged 变更 package 的测试，秒级完成。
# 用意：pre-commit 阶段拦截"测试文件编译不过"和"单测断言失败"，
#       避免 push 后 CI 返工。
#
# 跳过：SKIP=incremental-go-test git commit ...

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

# 只收集 staged 的 .go 文件
staged=$(git diff --cached --name-only --diff-filter=ACM | grep '\.go$' || true)

if [[ -z "$staged" ]]; then
  exit 0
fi

# 提取唯一的 package 目录
pkgs=$(echo "$staged" | while read -r f; do dirname "$f"; done | sort -u)

passed=true
for pkg in $pkgs; do
  [[ -d "$pkg" ]] || continue
  ls "$pkg"/*.go >/dev/null 2>&1 || continue
  # 跳过需要基础设施的 e2e
  [[ "$pkg" == "test/e2e" ]] && continue

  if ! go test -short -count=1 -p "$(bash scripts/quality/go-parallelism.sh)" "./$pkg" 2>&1; then
    passed=false
  fi
done

if [[ "$passed" != true ]]; then
  echo '' >&2
  echo '❌ 增量 Go 测试失败，请在 push 前修复。' >&2
  echo '   跳过本次检查：SKIP=incremental-go-test git commit ...' >&2
  exit 1
fi
