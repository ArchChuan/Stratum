#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

if [[ "${CI:-}" == "true" && -z "${STRATUM_TEST_POSTGRES_URL:-}" ]]; then
  echo 'STRATUM_TEST_POSTGRES_URL is required for the CI tool permission harness' >&2
  exit 2
fi

go test \
  ./internal/agent/domain \
  ./internal/agent/application/... \
  ./internal/mcp/... \
  ./api/http/... \
  -run 'Test.*(ToolAuthorization|ToolApproval|ToolExecutionGuard|ToolResultGuard|ToolPermission|ForgedToolCall|ApprovalEvent|FakeServer|DeterministicFakeServer)' \
  -count=1

go test ./internal/agent/infrastructure/persistence \
  -run 'TestToolApprovalEncryptedDecisionAndExactlyOnceExecution|TestToolPermissionHarnessIsolatesApprovalAcrossTenantSchemas' \
  -count=1

npm --prefix web test -- \
  src/modules/agent/hooks/__tests__/useChatPage.test.tsx \
  src/modules/agent/pages/__tests__/AgentChatMobile.test.tsx
