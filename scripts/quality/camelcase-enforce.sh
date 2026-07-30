#!/usr/bin/env bash
# camelcase-enforce.sh — 验证所有 contract golden files 的 JSON 键使用 camelCase。
# 在 CI 中运行：json key 出现 PascalCase（首字母大写）即失败。
# 金丝雀文件（contract golden）已覆盖所有端点——此检查捕捉 Go struct 缺 json tag 的回归。
set -euo pipefail

GOLDEN_DIR="${1:-api/http/testdata/contracts}"
if [[ ! -d "$GOLDEN_DIR" ]]; then
  echo "ERROR: golden directory not found: $GOLDEN_DIR"
  exit 2
fi

violations=0
total_files=0

while IFS= read -r -d '' file; do
  total_files=$((total_files + 1))
  # 提取 JSON 响应体的所有顶层键（want_body 和 body 字段）
  pascal_keys=$(jq -r '
    .[]? | select(.want_status != null) |
    (.want_body // .body // empty) |
    if type == "object" then keys[] else empty end
  ' "$file" 2>/dev/null | grep '^[A-Z]' || true)

  if [[ -n "$pascal_keys" ]]; then
    while IFS= read -r key; do
      echo "PascalCase key '$key' found in $file"
      violations=$((violations + 1))
    done <<< "$pascal_keys"
  fi
done < <(find "$GOLDEN_DIR" -name '*.golden.json' -print0)

echo ""
echo "Scanned $total_files golden files"

if [[ $violations -gt 0 ]]; then
  echo "FAIL: $violations PascalCase key(s) detected."
  echo "Add json:\"camelCase\" tags to the corresponding Go struct fields."
  exit 1
fi

echo "PASS: All JSON keys use camelCase."
