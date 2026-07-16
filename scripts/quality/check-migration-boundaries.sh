#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
TENANT_SCHEMA="${ROOT_DIR}/pkg/storage/postgres/tenant_schema.sql"
MIGRATION_DIR="${ROOT_DIR}/pkg/migration/sql"

if [[ ! -f "${TENANT_SCHEMA}" ]]; then
    echo "tenant schema not found: ${TENANT_SCHEMA}" >&2
    exit 1
fi

if [[ ! -d "${MIGRATION_DIR}" ]]; then
    echo "migration directory not found: ${MIGRATION_DIR}" >&2
    exit 1
fi

mapfile -t tenant_tables < <(
    grep -oE 'CREATE TABLE (IF NOT EXISTS )?[a-z_]+' "${TENANT_SCHEMA}" |
        awk '{print $NF}' |
        sort -u
)

violations=0

strip_sql_comments_and_strings() {
    awk '
        BEGIN { in_block = 0; in_string = 0 }
        {
            line = $0
            output = ""
            for (i = 1; i <= length(line); i++) {
                char = substr(line, i, 1)
                next_char = substr(line, i + 1, 1)

                if (in_block) {
                    if (char == "*" && next_char == "/") {
                        in_block = 0
                        i++
                    }
                    continue
                }

                if (in_string) {
                    if (char == "\047" && next_char == "\047") {
                        i++
                    } else if (char == "\047") {
                        in_string = 0
                    }
                    continue
                }

                if (char == "/" && next_char == "*") {
                    in_block = 1
                    i++
                } else if (char == "-" && next_char == "-") {
                    break
                } else if (char == "\047") {
                    in_string = 1
                } else {
                    output = output char
                }
            }
            print output
        }
    '
}

strip_sql_comments() {
    awk '
        BEGIN { in_block = 0; in_string = 0 }
        {
            line = $0
            output = ""
            for (i = 1; i <= length(line); i++) {
                char = substr(line, i, 1)
                next_char = substr(line, i + 1, 1)

                if (in_block) {
                    if (char == "*" && next_char == "/") {
                        in_block = 0
                        i++
                    }
                    continue
                }

                if (in_string) {
                    output = output char
                    if (char == "\047" && next_char == "\047") {
                        output = output next_char
                        i++
                    } else if (char == "\047") {
                        in_string = 0
                    }
                    continue
                }

                if (char == "/" && next_char == "*") {
                    in_block = 1
                    i++
                } else if (char == "-" && next_char == "-") {
                    break
                } else {
                    output = output char
                    if (char == "\047") {
                        in_string = 1
                    }
                }
            }
            print output
        }
    '
}

while IFS= read -r -d '' migration; do
    migration_name="$(basename "${migration}")"
    case "${migration_name}" in
        004_agent_executions.* | 005_move_agent_executions.*)
            # Historical bridge migrations created and moved this table before
            # the tenant-only boundary became the enforced steady state.
            continue
            ;;
    esac

    comment_free_sql="$(strip_sql_comments <"${migration}" | tr '\n' ' ')"
    executable_sql="$(strip_sql_comments_and_strings <"${migration}" | tr '\n' ' ')"
    for table in "${tenant_tables[@]}"; do
        table_context="(TABLE[[:space:]]+(IF[[:space:]]+(NOT[[:space:]]+)?EXISTS[[:space:]]+)?|INTO[[:space:]]+|UPDATE[[:space:]]+|FROM[[:space:]]+|JOIN[[:space:]]+|REFERENCES[[:space:]]+|ON[[:space:]]+COLUMN[[:space:]]+|ON[[:space:]]+|TRUNCATE[[:space:]]+(TABLE[[:space:]]+)?)"
        qualified_table="(\"?[a-z_][a-z0-9_]*\"?\.)?\"?${table}\"?([^a-zA-Z0-9_]|$)"
        direct_reference="${table_context}(ONLY[[:space:]]+)?${qualified_table}"
        comma_reference="(FROM|UPDATE|USING)[^;]*,[[:space:]]*(ONLY[[:space:]]+)?${qualified_table}"
        dynamic_reference="EXECUTE[[:space:]]+[^;]*${qualified_table}"
        if grep -qiE "${direct_reference}|${comma_reference}" <<<"${executable_sql}" ||
            grep -qiE "${dynamic_reference}" <<<"${comment_free_sql}"; then
            printf 'tenant table %q must not be referenced by numbered migration %s\n' \
                "${table}" "${migration#"${ROOT_DIR}/"}" >&2
            violations=$((violations + 1))
        fi
    done
done < <(find "${MIGRATION_DIR}" -maxdepth 1 -type f -regextype posix-extended \
    -regex '.*/[0-9]+.*\.sql' -print0 | sort -z)

if ((violations > 0)); then
    exit 1
fi

echo "migration boundaries passed"
