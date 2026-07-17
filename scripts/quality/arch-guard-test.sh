#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECKER="${SCRIPT_DIR}/arch-guard.sh"

TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "${TMP_ROOT}"' EXIT

WIRING_DIR="${TMP_ROOT}/api/wiring"
INFRA_DIR="${TMP_ROOT}/internal/skill/infrastructure"
mkdir -p "${WIRING_DIR}" "${INFRA_DIR}"

# 期望通过（exit0）：wiring 文件仅做 thin adapter，无裸 SQL
cat >"${WIRING_DIR}/clean.go" <<'GO'
package wiring

func newSkillCandidateManager(svc skillapp.VersionService) *skillCandidateManager {
	return &skillCandidateManager{svc: svc}
}
GO
bash "${CHECKER}" "${WIRING_DIR}/clean.go"

# 期望失败（exit1）：wiring 文件散写裸 SQL，逐个动词都要拦截
declare -a violations=(
	'tx.QueryRow(ctx, "SELECT agent_id FROM agent_skill_links WHERE skill_id=$1", id)'
	'tx.Query(ctx, "SELECT id FROM skills")'
	'tx.Exec(ctx, "UPDATE skills SET name=$1", name)'
	'tx.SendBatch(ctx, batch)'
	'tx.CopyFrom(ctx, tableName, cols, src)'
	'conn.QueryRow(ctx, "SELECT 1")'
	'conn.Exec(ctx, "DELETE FROM skills")'
)

for stmt in "${violations[@]}"; do
	cat >"${WIRING_DIR}/bad.go" <<GO
package wiring

func loadSnapshot() {
	${stmt}
}
GO
	if bash "${CHECKER}" "${WIRING_DIR}/bad.go" >/dev/null 2>&1; then
		echo "expected wiring raw-SQL violation to fail: ${stmt}" >&2
		exit 1
	fi
done
rm "${WIRING_DIR}/bad.go"

# 期望通过（exit0）：wiring 目录下的 *_test.go 豁免（测试可用裸 SQL 造夹具）
cat >"${WIRING_DIR}/snapshot_test.go" <<'GO'
package wiring

func TestLoad(t *testing.T) {
	tx.QueryRow(ctx, "SELECT 1")
}
GO
bash "${CHECKER}" "${WIRING_DIR}/snapshot_test.go"

# 期望通过（exit0）：非 wiring 路径（infrastructure repo）裸 SQL 是合法的，不该处理
cat >"${INFRA_DIR}/skill_repo.go" <<'GO'
package infrastructure

func (r *SkillRepo) find(ctx context.Context) {
	tx.QueryRow(ctx, "SELECT id FROM skills WHERE id=$1", id)
}
GO
bash "${CHECKER}" "${INFRA_DIR}/skill_repo.go"

# 期望通过（exit0）：非 .go 文件直接跳过
cat >"${WIRING_DIR}/notes.md" <<'MD'
tx.QueryRow(ctx, "SELECT 1")
MD
bash "${CHECKER}" "${WIRING_DIR}/notes.md"

# 期望失败（exit1）：多文件混合（干净 + 违规 + 豁免）只要有一个违规就整体失败
cat >"${WIRING_DIR}/bad.go" <<'GO'
package wiring

func loadSnapshot() {
	tx.QueryRow(ctx, "SELECT 1")
}
GO
if bash "${CHECKER}" \
	"${WIRING_DIR}/clean.go" \
	"${WIRING_DIR}/snapshot_test.go" \
	"${INFRA_DIR}/skill_repo.go" \
	"${WIRING_DIR}/bad.go" >/dev/null 2>&1; then
	echo "expected mixed batch with one violation to fail" >&2
	exit 1
fi

echo "arch-guard tests passed"
