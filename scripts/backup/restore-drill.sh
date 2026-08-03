#!/usr/bin/env bash

set -euo pipefail

# Restore a gzip-compressed custom-format pg_dump into a scratch database and
# validate that public tables, tenant schemas, and key rows survive. Fails
# closed when any check fails.
#
# Usage:
#   PGHOST=127.0.0.1 PGPORT=5432 PGUSER=postgres PGPASSWORD=postgres \
#     bash scripts/backup/restore-drill.sh <dump.sql.gz> [report.txt]

DUMP_GZ="${1:?usage: restore-drill.sh <dump.sql.gz> [report.txt]}"
REPORT="${2:-backup-drill-report.txt}"
: "${PGHOST:=127.0.0.1}"
: "${PGPORT:=5432}"
: "${PGUSER:=postgres}"
: "${PGPASSWORD:=postgres}"
: "${PGDATABASE:=stratum_drill}"

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "restore-drill: required command unavailable: $1" >&2
    exit 1
  }
}
for command_name in psql pg_restore gunzip; do
  require_command "${command_name}"
done

export PGPASSWORD
: >"${REPORT}"

fail() {
  echo "restore-drill: $*" >&2
  echo "FAIL: $*" >>"${REPORT}"
  exit 1
}

psql -h "${PGHOST}" -p "${PGPORT}" -U "${PGUSER}" -d postgres -v ON_ERROR_STOP=1 \
  -c "DROP DATABASE IF EXISTS ${PGDATABASE};" \
  -c "CREATE DATABASE ${PGDATABASE};" >/dev/null \
  || fail "cannot create drill database"

gunzip -c "${DUMP_GZ}" \
  | pg_restore -h "${PGHOST}" -p "${PGPORT}" -U "${PGUSER}" \
      -d "${PGDATABASE}" --no-owner --no-privileges \
  || fail "pg_restore failed"

run_query() {
  psql -h "${PGHOST}" -p "${PGPORT}" -U "${PGUSER}" \
    -d "${PGDATABASE}" -tAc "$1"
}

tenants="$(run_query 'SELECT count(*) FROM public.tenants;')"
users="$(run_query 'SELECT count(*) FROM public.users;')"
tenant_schemas="$(run_query "SELECT count(*) FROM information_schema.schemata WHERE schema_name LIKE 'tenant\\_%';")"

[[ "${tenants}" =~ ^[0-9]+$ && "${tenants}" -gt 0 ]] \
  || fail "public.tenants rows: ${tenants:-missing}"
[[ "${users}" =~ ^[0-9]+$ && "${users}" -gt 0 ]] \
  || fail "public.users rows: ${users:-missing}"
[[ "${tenant_schemas}" =~ ^[0-9]+$ && "${tenant_schemas}" -gt 0 ]] \
  || fail "tenant schemas: ${tenant_schemas:-missing}"

while IFS= read -r schema_name; do
  [[ -n "${schema_name}" ]] || continue
  table_count="$(run_query "SELECT count(*) FROM information_schema.tables WHERE table_schema = '${schema_name}';")"
  [[ "${table_count}" =~ ^[0-9]+$ && "${table_count}" -gt 0 ]] \
    || fail "tenant schema ${schema_name} has no tables"
done < <(run_query "SELECT schema_name FROM information_schema.schemata WHERE schema_name LIKE 'tenant\\_%' ORDER BY 1;")

{
  echo "PASS"
  echo "public.tenants=${tenants}"
  echo "public.users=${users}"
  echo "tenant_schemas=${tenant_schemas}"
  echo "restored_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
} >>"${REPORT}"
echo "restore drill passed"
cat "${REPORT}"
