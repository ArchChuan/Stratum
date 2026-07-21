#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

if HITS="$(rg -n 'agent_executions|agent_tool_traces|agent_trace_events' internal api \
  --glob '*.go' --glob '!*_test.go')"; then
	echo "runtime code still references PostgreSQL Agent observation tables:"
	echo "$HITS"
	exit 1
fi
