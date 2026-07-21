#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VALIDATOR="${ROOT}/scripts/quality/validate-remote-http-base-url.sh"

accept() {
    local value="$1"
    if ! /usr/bin/bash "${VALIDATOR}" "${value}" >/dev/null 2>&1; then
        echo "expected URL to be accepted: ${value}" >&2
        exit 1
    fi
}

reject() {
    local value="$1"
    if /usr/bin/bash "${VALIDATOR}" "${value}" >/dev/null 2>&1; then
        echo "expected URL to be rejected: ${value}" >&2
        exit 1
    fi
}

accept 'http://203.0.113.10:6879'
reject ''
reject 'https://203.0.113.10:6879'
reject 'http://demo.example.com:6879'
reject 'http://203.0.113.10'
reject 'http://203.0.113.10:80'
reject 'http://203.0.113.10:6879/'
reject 'http://user@203.0.113.10:6879'
reject 'http://203.0.113.999:6879'
reject 'http://127.0.0.1:6879'
reject 'http://0.0.0.0:6879'
reject 'http://224.0.0.1:6879'
reject 'http://255.255.255.255:6879'

echo 'remote HTTP base URL validation tests passed'
