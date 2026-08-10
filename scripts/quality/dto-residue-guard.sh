#!/usr/bin/env bash
# dto-residue-guard: fail if any hand-written DTO residue survives the proto
# migration (§8 阶段 3). The proto contract in proto/ is the single source
# of truth; generated output lives in gen/ dirs and is not committed.
#
# migrated_types 清单 = T8-T19 各批实际从 web/src/modules 删除的前端契约 interface
# 声明(被 gen 类型 alias import/re-export 替换)。任何新批删除的契约类型都必须
# 登记进本清单,guard 才不失效。排除项:
#   - ExecuteAgentPayload(T9):被改写为 extends GenExecuteAgentRequest(wire-only
#     conversation_id),仍合法声明,登记会导致误报。
#   - ExperimentResponse:evaluation 模块 zod 双源保留 type alias,未被 proto 替换。
set -euo pipefail

cd "$(dirname "$0")/../.."

fail=0

# 1. api/http/dto/ must contain nothing but the generated gen/ subdir
leftover_dto=$(find api/http/dto -maxdepth 1 -type f -name '*.go' | wc -l)
if [[ "$leftover_dto" -ne 0 ]]; then
  echo "error: hand-written DTO files remain in api/http/dto/:" >&2
  find api/http/dto -maxdepth 1 -type f -name '*.go' >&2
  fail=1
fi

# 2. no stale import of the old dto package (gen/ files are exempt —
#    they never import the package they live in)
stale=$(grep -rln '"github.com/byteBuilderX/stratum/api/http/dto"' \
  --include='*.go' api cmd internal pkg web 2>/dev/null | grep -v '/gen/' || true)
if [[ -n "$stale" ]]; then
  echo "error: stale imports of the old dto package:" >&2
  echo "$stale" >&2
  fail=1
fi

# 3. migrated frontend contract type declarations must not resurface in
#    web/src/modules (zod schemas are exempt — §7 keeps them hand-written).
#    只匹配声明形态:`interface X` 与 `type X =`/`type X<T> =`。import 行
#    (含 inline type 限定符,如 `type X,`/`type X as Y`)不含这些形态,不误报;
#    re-export(`export type { X }`)同样不匹配。
migrated_types=(
  'CreateCollabPayload'
  'MCPAuthConfigResponse'
  'MCPServerConfigResponse'
  'CreateSkillDraftPayload'
)
for name in "${migrated_types[@]}"; do
  hits=$(grep -rnE "interface ${name}\\b|type ${name}(<[^>]*>)?[[:space:]]*=" web/src/modules --include='*.ts' 2>/dev/null || true)
  if [[ -n "$hits" ]]; then
    echo "error: migrated contract type still declared: $name" >&2
    echo "$hits" >&2
    fail=1
  fi
done

if [[ "$fail" -ne 0 ]]; then
  exit 1
fi
echo "dto-residue-guard: clean"
