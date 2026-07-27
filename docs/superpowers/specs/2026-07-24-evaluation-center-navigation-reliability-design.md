# Evaluation Center Navigation Reliability Design

**Date:** 2026-07-24
**Status:** Approved
**Scope:** Evaluation center deep links, authentication loading diagnostics, and E2E dependency readiness

## Goal

Make the existing Skill-to-evaluation-center link preserve its resource scope, remove the known Ant Design loading warning,
and fail the evaluation E2E early when the installed frontend dependency tree is stale.

## Behavior

- `/evaluations?kind=<kind>&resource_id=<id>` initializes the resource-kind and resource-ID filters.
- Supported kinds are `skill`, `agent`, `mcp`, and `knowledge`. Unknown kinds are ignored instead of being sent to the API.
- Changing or clearing the resource-kind selector updates `kind` in the URL while preserving a valid `resource_id`.
- Browser back/forward navigation updates the visible filter and query scope.
- The resource ID remains a URL-only scope in this release; no additional first-viewport control is added.
- The private-route loading state remains centered without passing `tip` to a standalone Ant Design `Spin`.
- `scripts/e2e/evaluation-evolution.sh` verifies the installed `@xyflow/react` package before starting containers and reports
  a safe `npm ci` remediation without modifying dependencies itself.

## Boundaries

- No backend API, persistence, authorization, evaluation state-machine, or resource revision behavior changes.
- No query parameter may contain credentials or payload content; only the existing closed resource kind and resource ID are used.
- Existing query parameters unrelated to these filters are preserved.

## Verification

- Component tests prove initialization, invalid-kind rejection, URL synchronization, and history navigation.
- Private-route tests fail on the previous Ant Design warning and pass without suppressing console output.
- A shell test proves the E2E script rejects a missing dependency before Docker startup.
- Frontend lint/build and the real isolated evaluation E2E must pass.
