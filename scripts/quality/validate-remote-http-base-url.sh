#!/usr/bin/env bash

set -euo pipefail

invalid() {
    echo 'invalid PUBLIC_BASE_URL: expected http://<public-ip>:6879' >&2
    exit 1
}

[[ "$#" -eq 1 ]] || invalid

url="$1"
if [[ ! "${url}" =~ ^http://([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3}):6879$ ]]; then
    invalid
fi

octets=("${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}" "${BASH_REMATCH[4]}")
for octet in "${octets[@]}"; do
    ((10#${octet} <= 255)) || invalid
done

first="${octets[0]}"
if ((10#${first} == 0 || 10#${first} == 127 || 10#${first} >= 224)); then
    invalid
fi
