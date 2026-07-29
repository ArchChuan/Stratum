# Dashboard Overview Metrics Design

## Goal

Extend the tenant Dashboard from four to eight resource metrics while keeping the page focused on system state. Add model provider count, tenant member count, workflow count, and the number of user messages sent to agents during the rolling 168 hours before the request.

## Confirmed Semantics

- Preserve the existing Agent, skill, knowledge workspace, and MCP server counts.
- Model provider count means all configured providers in the current tenant, regardless of enabled state.
- Tenant member count means all current rows in `public.tenant_members` for the current tenant.
- Workflow count means all workflow definitions in the current tenant, regardless of publication state.
- Recent agent conversation count means `chat_messages` rows whose role is `user` and whose `created_at` is at or after the request database time minus 168 hours.
- All eight values are tenant scoped and visible to tenant members. The response exposes counts only, not restricted provider or member details.

## Evidence And Constraints

- The current Dashboard loads four complete resource lists in `useDashboardPage` and derives their lengths in the browser.
- Provider list access is registered under the tenant-admin route group, so reusing it would make the Dashboard incomplete for ordinary members.
- Tenant members already have a repository count query against `public.tenant_members`.
- Workflow definitions, providers, agents, skills, MCP servers, knowledge workspaces, and chat messages are tenant-schema data. Their reads must execute inside the tenant boundary.
- The governed Obsidian evidence protocol confirms that repository code and runtime tests are authoritative for current project behavior. The Vault search produced no verified Dashboard-specific design that overrides repository constraints.

## Options Considered

### Dedicated Dashboard Overview Endpoint (selected)

Add one member-readable endpoint that returns the eight counts from a dedicated read model. This avoids transferring full lists, gives every role a consistent response, and provides one atomic HTTP contract. The trade-off is an intentionally coupled reporting query, contained within the Platform context.

### Reuse Existing List Endpoints

The frontend could request each resource list and derive totals. This minimizes backend code but increases requests and payloads, cannot reuse the admin-only provider list for members, and still needs a new query for recent user messages.

### Add Count Endpoints To Every Context

Each business context could expose its own count endpoint and the frontend could combine them. This preserves context ownership but adds excessive API surface and partial-failure behavior for a low-frequency overview page.

## Backend Design

Register `GET /dashboard/overview` under JWT authentication, tenant context injection, and the existing `member` role gate.

Add a Platform Dashboard read model with:

- a domain value containing the eight integer counts;
- a consumer-owned repository port accepting an explicit tenant ID and 168-hour cutoff/window semantics;
- an application service that validates the tenant ID, invokes the repository, and propagates wrapped failures;
- a PostgreSQL repository that runs tenant-table counts through the established tenant transaction helper and schema-qualifies `public.tenant_members`;
- a thin HTTP handler and DTO that read tenant context, invoke the service, and render JSON.

The repository uses database time for the rolling window so application-host clock skew cannot change the boundary. No table or migration changes are required. A failure in any count fails the entire request; the API does not return a mixture of current and fabricated zero values.

Proposed response:

```json
{
  "agents": 0,
  "skills": 0,
  "knowledge_workspaces": 0,
  "mcp_servers": 0,
  "model_providers": 0,
  "tenant_members": 0,
  "workflows": 0,
  "agent_user_messages_7d": 0
}
```

## Frontend Design

Add a typed Dashboard API method using the shared Axios client. `useDashboardPage` makes one overview request, replaces all counts together on success, and keeps stable zero defaults while loading.

Render eight statistic cards in this order:

1. Agent
2. 技能
3. 知识库
4. MCP 服务器
5. 模型厂商
6. 租户成员
7. 工作流
8. 近七日 Agent 对话

Use four columns on large screens, two on tablet widths, and one on mobile. Reuse the existing statistic-card visual language and Ant Design icons. On failure, show the project-standard non-expiring error notification and do not present partial counts as successful data.

## Error And Security Behavior

- Missing or invalid tenant context fails closed.
- The route requires at least the tenant `member` role.
- Repository methods carry the explicit tenant ID and use the tenant execution boundary for tenant-schema tables.
- The response contains aggregate integers only and never returns provider configuration, member identity, message content, credentials, or raw storage errors.
- The common error middleware retains the frozen `{"error":"..."}` response shape.

## Testing And Acceptance

Follow test-driven development with a failing test before each production behavior.

- Repository tests verify all eight counts, cross-tenant isolation, exclusion of non-user messages, inclusion inside the 168-hour window, and exclusion outside the boundary.
- Application and handler tests verify explicit tenant propagation, failure propagation, member access, missing-tenant rejection, and response fields.
- HTTP contract coverage freezes `GET /dashboard/overview`.
- Frontend API, hook, and page tests verify one request, all eight values, loading/error behavior, labels, and responsive column spans.
- Run targeted Go and Vitest tests, `go vet && go test -short ./...`, `make fe-lint`, `make fe-build`, and `make risk-guardrails`.
- Run the risk acceptance selector for all changed files, then the required `make e2e-system-short` Dashboard path and `make e2e-attestation-check`. Escalate to soak only if the selector reports `soak`.

## Out Of Scope

- Charts or day-by-day trends.
- Click-through navigation from statistic cards.
- Caching, materialized views, or new database indexes without runtime evidence that the count query needs them.
- Changing existing provider, member, workflow, or conversation management permissions.
