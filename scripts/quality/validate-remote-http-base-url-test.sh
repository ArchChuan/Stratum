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

accept 'https://203.0.113.10:8443'
reject ''
reject 'http://203.0.113.10:8443'
reject 'https://demo.example.com:8443'
reject 'https://203.0.113.10'
reject 'https://203.0.113.10:443'
reject 'https://203.0.113.10:8443/'
reject 'https://user@203.0.113.10:8443'
reject 'https://203.0.113.999:8443'
reject 'https://127.0.0.1:8443'
reject 'https://0.0.0.0:8443'
reject 'https://10.0.0.1:8443'
reject 'https://172.16.0.1:8443'
reject 'https://192.168.0.1:8443'
reject 'https://169.254.1.1:8443'
reject 'https://100.64.0.1:8443'
reject 'https://224.0.0.1:8443'
reject 'https://255.255.255.255:8443'

echo 'remote HTTPS base URL validation tests passed'
