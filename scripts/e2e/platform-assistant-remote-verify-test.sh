#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
work_dir=$(mktemp -d)
trap '/usr/bin/rm -rf -- "$work_dir"' EXIT

if grep -q 'jsonb_object_length' "$ROOT/scripts/e2e/platform-assistant-remote-verify.sh"; then
    echo 'remote verifier uses unsupported jsonb_object_length' >&2
    exit 1
fi

cat >"$work_dir/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

output_file=""
while (($# > 0)); do
    case "$1" in
        --output)
            output_file="$2"
            shift 2
            ;;
        --write-out)
            shift 2
            ;;
        *)
            shift
            ;;
    esac
done

printf '<!doctype html><title>Stratum</title>' >"$output_file"
printf '200'
EOF
chmod +x "$work_dir/curl"

set +e
output=$(PATH="$work_dir:$PATH" bash "$ROOT/scripts/e2e/platform-assistant-remote-verify.sh" \
    http://stratum.example test@example 2>&1)
status=$?
set -e

if [[ $status -eq 0 ]]; then
    echo 'remote verifier accepted an HTML health response' >&2
    exit 1
fi
if [[ "$output" != *'"check":"public_health_contract"'* ]]; then
    echo "remote verifier did not reject the HTML response at the health contract: $output" >&2
    exit 1
fi

echo 'platform assistant remote verifier tests passed'
