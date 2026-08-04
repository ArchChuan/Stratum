#!/usr/bin/env bash

set -euo pipefail

# Dump the stratum PostgreSQL database to a gzip-compressed custom-format file.
# The password is read from the container's own POSTGRES_PASSWORD env var
# (sourced from the stratum-secrets secret); it never appears on the runner.
#
# Usage:
#   BACKUP_NAMESPACE=stratum BACKUP_DEPLOYMENT=stratum-postgresql \
#     BACKUP_OUTPUT=/tmp/backup.sql.gz bash scripts/backup/backup-postgres.sh

NAMESPACE="${BACKUP_NAMESPACE:-stratum}"
DEPLOYMENT="${BACKUP_DEPLOYMENT:-stratum-postgresql}"
OUTPUT="${BACKUP_OUTPUT:-backup.sql.gz}"

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "backup-postgres: required command unavailable: $1" >&2
    exit 1
  }
}
for command_name in kubectl gzip sha256sum; do
  require_command "${command_name}"
done

tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

kubectl exec "deploy/${DEPLOYMENT}" -n "${NAMESPACE}" -- \
  sh -c 'PGPASSWORD="${POSTGRES_PASSWORD}" pg_dump -Fc -U stratum -d stratum' \
  >"${tmp_dir}/dump.pg"
gzip -c "${tmp_dir}/dump.pg" >"${OUTPUT}"
sha256sum "${OUTPUT}" >"${OUTPUT}.sha256"
echo "backup complete: ${OUTPUT} ($(du -h "${OUTPUT}" | cut -f1))"
