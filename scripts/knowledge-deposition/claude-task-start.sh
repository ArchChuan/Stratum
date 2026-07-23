#!/usr/bin/env bash
set -euo pipefail
adapter_failure() { trap - ERR; printf '%s\n' '{"decision":"block","reason":"knowledge deposition: internal adapter failure","continue":false,"suppressOutput":false}'; exit 0; }
trap adapter_failure ERR
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/hook-core.sh"
knowledge_start claude claude
trap - ERR
