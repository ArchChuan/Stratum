#!/usr/bin/env bash
set -euo pipefail

# 自测：增量 Go 测试脚本的行为契约

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="${ROOT}/scripts/quality/incremental-go-test.sh"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf "${TEST_ROOT}"' EXIT

cd "${ROOT}"

# --- no staged Go files → pass silently ---
output=$(bash "${SCRIPT}" 2>&1) || { echo 'unexpected failure with no staged Go files' >&2; exit 1; }
[[ -z "$output" ]] || { echo "expected empty output, got: ${output}" >&2; exit 1; }

# --- staged Go file with syntax error → fail ---
bad_file="scripts/quality/testdata/bogus_syntax_test.go"
mkdir -p "$(dirname "${bad_file}")"
cat >"${ROOT}/${bad_file}" <<'EOF'
package bogus
func Bad(
EOF

git -C "${ROOT}" add "${bad_file}" 2>/dev/null

set +e
bash "${SCRIPT}" 2>/dev/null
status=$?
set -e
git -C "${ROOT}" reset HEAD -- "${bad_file}" 2>/dev/null || true
rm -f "${ROOT}/${bad_file}"

if [[ "${status}" -eq 0 ]]; then
  echo 'incremental test did not catch a syntax error in staged Go file' >&2
  exit 1
fi

echo 'incremental go test behavior passed'
