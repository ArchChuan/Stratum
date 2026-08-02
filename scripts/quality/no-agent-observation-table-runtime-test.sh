#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

if HITS="$(find internal api -name '*.go' ! -name '*_test.go' \
  -exec grep -nP 'agent_executions|agent_tool_traces|agent_trace_events' {} +)"; then
	echo "runtime code still references PostgreSQL Agent observation tables:"
	echo "$HITS"
	exit 1
fi
