#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECKER="${SCRIPT_DIR}/check-migration-boundaries.sh"

TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "${TMP_ROOT}"' EXIT

mkdir -p "${TMP_ROOT}/pkg/migration/sql" "${TMP_ROOT}/pkg/storage/postgres"

cat >"${TMP_ROOT}/pkg/storage/postgres/tenant_schema.sql" <<'SQL'
CREATE TABLE IF NOT EXISTS memory_entries (
    id UUID PRIMARY KEY
);
SQL

cat >"${TMP_ROOT}/pkg/migration/sql/001_public.up.sql" <<'SQL'
ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name TEXT;
SQL

bash "${CHECKER}" "${TMP_ROOT}"

cat >"${TMP_ROOT}/pkg/migration/sql/002_tenant.up.sql" <<'SQL'
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS importance DOUBLE PRECISION;
SQL

if bash "${CHECKER}" "${TMP_ROOT}" >/dev/null 2>&1; then
    echo "expected tenant-table migration violation to fail" >&2
    exit 1
fi

rm "${TMP_ROOT}/pkg/migration/sql/002_tenant.up.sql"

declare -a indirect_references=(
    'CREATE INDEX idx_memory_importance ON memory_entries (importance);'
    'ALTER TABLE users ADD COLUMN memory_id UUID REFERENCES memory_entries(id);'
    'SELECT * FROM tenant_example.memory_entries;'
    'SELECT * FROM users, memory_entries;'
    "COMMENT ON COLUMN memory_entries.id IS 'importance';"
    "DO \$\$ BEGIN EXECUTE 'ALTER TABLE memory_entries ADD COLUMN x INTEGER'; END \$\$;"
)

for sql in "${indirect_references[@]}"; do
    printf '%s\n' "${sql}" >"${TMP_ROOT}/pkg/migration/sql/002_tenant.up.sql"
    if bash "${CHECKER}" "${TMP_ROOT}" >/dev/null 2>&1; then
        echo "expected indirect tenant-table reference to fail: ${sql}" >&2
        exit 1
    fi
done

cat >"${TMP_ROOT}/pkg/migration/sql/002_tenant.up.sql" <<'SQL'
/*
Example only:
ALTER TABLE memory_entries ADD COLUMN ignored INTEGER;
*/
SELECT 1;
COMMENT ON TABLE users IS 'memory_entries is tenant-scoped';
ALTER TABLE users ADD COLUMN memory_entries JSONB;
SQL

bash "${CHECKER}" "${TMP_ROOT}"

echo "migration boundary checker tests passed"
