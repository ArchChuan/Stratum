#!/usr/bin/env bash
# hook 自测（纯 bash，无 bats 依赖）。CI 可直接调用：bash .claude/hooks/run-tests.sh
# 断言每个 guard 对 (输入 → permissionDecision) 的映射，防止"路径错了没人知道"重演。

set -uo pipefail
cd "$(dirname "$0")"

PASS=0
FAIL=0

# assert <名称> <期望子串> <脚本> <stdin-json>
assert() {
  local name="$1" want="$2" script="$3" input="$4"
  local out
  out="$(printf '%s' "$input" | bash "$script" 2>&1)"
  if printf '%s' "$out" | grep -qF "$want"; then
    PASS=$((PASS + 1))
    printf '  ✓ %s\n' "$name"
  else
    FAIL=$((FAIL + 1))
    printf '  ✗ %s\n    want substring: %s\n    got: %s\n' "$name" "$want" "$out"
  fi
}

echo "== guard-bash =="
assert "rm -rf 拦截"        '"permissionDecision":"deny"'  guard-bash.sh '{"tool_input":{"command":"rm -rf /tmp/x"}}'
assert "sudo 拦截"          '"permissionDecision":"deny"'  guard-bash.sh '{"tool_input":{"command":"sudo apt install"}}'
assert "git status 放行"    '"permissionDecision":"allow"' guard-bash.sh '{"tool_input":{"command":"git status"}}'
assert "confirm 不误伤"     '"permissionDecision":"allow"' guard-bash.sh '{"tool_input":{"command":"echo confirm"}}'

echo "== guard-fs =="
assert "/etc 拦截"          '"permissionDecision":"deny"'  guard-fs.sh '{"tool_input":{"file_path":"/etc/hosts"}}'
assert ".ssh 拦截"          '"permissionDecision":"deny"'  guard-fs.sh '{"tool_input":{"file_path":"/home/u/.ssh/id_rsa"}}'
assert "prod.yaml 写拦截"   '"permissionDecision":"deny"'  guard-fs.sh '{"tool_name":"Write","tool_input":{"file_path":"config/prod.yaml"}}'
assert "源码写放行"         '"permissionDecision":"allow"' guard-fs.sh '{"tool_name":"Write","tool_input":{"file_path":"internal/foo.go"}}'
assert "读凭据拦截"         '"permissionDecision":"deny"'  guard-fs.sh '{"tool_name":"Read","tool_input":{"file_path":"/home/u/.aws/credentials"}}'
assert "读 prod.yaml 放行"  '"permissionDecision":"allow"' guard-fs.sh '{"tool_name":"Read","tool_input":{"file_path":"config/prod.yaml"}}'
assert "读 /etc 放行"       '"permissionDecision":"allow"' guard-fs.sh '{"tool_name":"Read","tool_input":{"file_path":"/etc/hosts"}}'

echo "== check-migration-tenant-tables =="
assert "tenant 表拦截"      '"permissionDecision":"deny"'  check-migration-tenant-tables.sh '{"tool_input":{"file_path":"internal/migration/sql/099_x.sql","content":"ALTER TABLE memory_entries ADD COLUMN c int;"}}'
assert "public 表放行"      '"permissionDecision":"allow"' check-migration-tenant-tables.sh '{"tool_input":{"file_path":"internal/migration/sql/099_x.sql","content":"ALTER TABLE users ADD COLUMN c int;"}}'
assert "非迁移文件放行"     '"permissionDecision":"allow"' check-migration-tenant-tables.sh '{"tool_input":{"file_path":"internal/foo.go","content":"package foo"}}'

echo
printf 'Result: %d passed, %d failed\n' "$PASS" "$FAIL"
[[ "$FAIL" -eq 0 ]]
