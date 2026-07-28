# Platform Assistant Model Settings E2E

Date: 2026-07-28

## Scope

- New and blank historical platform-assistant models default to `glm-5.2`.
- Tenant overrides survive tenant schema replay.
- Tenant admins manage the model from the Agent edit page.
- Agent chat has no independent model-settings entry.
- Missing provider credentials remain visible and cannot be submitted as a valid configuration.

## Evidence

### Database

Ran the PostgreSQL integration tests against the local test database:

```text
TestProvisionTenantSchemaSystemAssistantIsIdempotent: PASS
TestProvisionTenantSchemaSystemAssistantModelBackfillPreservesTenantChoice/empty_managed_model: PASS
TestProvisionTenantSchemaSystemAssistantModelBackfillPreservesTenantChoice/tenant_override: PASS
```

The tests create unique temporary tenant schemas and clean them after completion. They prove whitespace-only models become
`glm-5.2`, while `qwen-plus` remains unchanged after schema replay.

### Browser and API

Ran one headless Chromium journey against the current worktree frontend and backend with a temporary guest identity:

```text
[desktop-1440] managed model is fail-visible and chat has no settings entry: PASS
```

The journey verified:

- a member receives HTTP 403 from `PUT /agents/system/settings`;
- after precise promotion of the temporary membership to admin, the user clicks the platform assistant's edit action from
  the Agent list;
- the managed edit page contains one model selector and no ordinary Agent configuration form;
- because the test tenant has no configured chat provider, the unavailable current model cannot be submitted;
- Agent chat exposes no `设置助手模型` or `助手设置` entry on desktop or at a 390 x 844 viewport;
- the temporary user is deleted in cleanup, with no retained test entity.

The environment had no tenant-available chat model, so a real successful model switch was not possible without adding a
provider credential. The available-model save path is covered by the component test and existing authenticated API
contract tests; no credential or fake provider configuration was introduced for this run.

## Regression Gates

- Agent frontend module: 41 tests passed.
- Frontend lint, TypeScript typecheck, and production build passed.
- Repository Go test queue passed.
- `make risk-guardrails` passed.
- `go vet` passed inside `stratum-verify go-full`.

`stratum-verify go-full` stopped at the repository dependency vulnerability gate because
`google.golang.org/grpc v1.79.2` is affected by `GO-2026-6061` and requires `v1.82.1`. This is outside the feature diff.
The repository does not currently contain the `--acceptance`, `e2e-system-short`, `e2e-system-soak`, or attestation targets
referenced by the current E2E skill, so those versioned acceptance gates could not be executed.
