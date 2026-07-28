# System Stateful IAM and OAuth E2E Design

## Goal

Complete the IAM portion of the repository-owned system stateful/soak suite with real headless Chromium operations and source-bound evidence. The suite must cover OAuth callback/exchange/register, logout, embed-model configuration, system-admin tenant creation, and tenant self-deletion without browser network mocks or persisted credentials.

## Boundaries

- Chromium initiates every primary user operation through visible UI controls.
- HTTP observation is limited to response evidence, setup, and explicit rejection assertions.
- SQL is limited to exact generated-identity setup, reconciliation, and cleanup in the dedicated E2E database.
- Four isolated browser contexts remain the authority boundaries: system admin, tenant admin, member A, and member B. OAuth registration and destructive tenant flows use disposable pages or contexts without weakening those boundaries.
- A capability is passed only after its UI action, HTTP result, and durable database outcome reconcile. Missing, skipped, or cleanup-failed evidence fails the whole run.

## Local OAuth Provider

The canonical runner starts a loopback-only fake GitHub OAuth process before the backend. It implements the protocol surface Stratum actually consumes:

- `GET /login/oauth/authorize` validates the configured client and callback, then redirects with the received state and a single-use authorization code.
- `POST /login/oauth/access_token` validates the client, redirect URI, and code, then returns an in-memory bearer token.
- `GET /user` and `GET /user/emails` require that token and return deterministic, per-run identities with a primary verified email.

The browser starts at Stratum's `/auth/github` endpoint. Stratum creates the OAuth state cookie, redirects to the fake provider, exchanges the code over real backend HTTP, and redirects to the frontend callback with only Stratum's opaque one-time exchange code. The frontend executes the existing `/auth/oauth/exchange` request and either restores a returning identity or routes a new identity through onboarding and `/auth/register`.

No provider code, provider token, refresh cookie, access token, password, or private key is logged or written to Playwright traces, screenshots, reports, or attestations. The fake provider keeps secrets in memory and reports only readiness and sanitized protocol failures.

## Configuration Safety

GitHub authorize, token, and user endpoints become explicit configuration values. Defaults remain GitHub's official HTTPS endpoints. Endpoint overrides are accepted only when `STRATUM_E2E_MODE=true` and every override resolves to an `http://127.0.0.1` or `http://localhost` URL. Invalid combinations fail startup; production cannot silently point OAuth at an arbitrary host.

The OAuth handler receives the authorize endpoint through wiring instead of hardcoding it. The existing GitHub client continues to receive token and user endpoints through constructor injection. The E2E runner supplies all three loopback URLs and owns the fake provider lifecycle.

## IAM Browser Journeys

### New OAuth identity

1. Open `/auth/github` in Chromium and follow the real redirect chain.
2. Observe the frontend callback exchanging the opaque code.
3. Confirm the new identity reaches onboarding.
4. Create a generated tenant through the form, causing `/auth/register`.
5. Reconcile verified email, user, tenant, owner membership, and active browser session in PostgreSQL and UI.

### Returning OAuth identity

Repeat the login entry with the registered provider identity, assert `/auth/oauth/exchange`, and confirm direct authenticated routing without a second user or tenant.

### Remaining mutations

- Logout uses the real user menu, observes `POST /auth/logout`, lands on `/login`, and verifies the prior refresh session cannot restore authentication.
- Embed model uses tenant settings UI, observes `PATCH /tenant/embed-model`, refreshes, reconciles the setting, and confirms the set-once control is locked.
- System-admin tenant creation uses a new modal on the existing admin tenant page. The form exposes name, slug, plan, and status; the resulting row and database record must agree.
- Tenant self-deletion uses a disposable tenant and the existing destructive confirmation, observes `DELETE /tenant`, confirms authenticated routing changes, and reconciles removal.

## Admin Tenant Creation UI

The admin tenant list gains one primary `创建租户` command. Its modal contains the four backend-supported fields, with required validation for name and slug, constrained plan/status choices, mutation loading state, and non-expiring error feedback. On success it closes, resets, refreshes server pagination, and shows a two-second success message. Desktop and mobile render the same command and contract.

## Cleanup and Failure Semantics

All generated identifiers are recorded as they are created. Cleanup removes only those exact identities and tenants, in a bounded transaction, after browser contexts close. Execution and cleanup failures are combined so cleanup cannot hide the original failure and the original failure cannot hide residual data. Provider and service processes always stop through runner traps.

The source digest is captured before services start and compared after browser execution. Only a passing safe-results document can generate an attestation, and the attestation checker must reject any source drift, missing capability, unsafe artifact, or residual entity.

## Acceptance

- Stateful unit/contract tests pass with no trace or screenshot artifacts enabled.
- `STATEFUL_E2E_PACKS=iam make e2e-system-short` passes every IAM capability through headless Chromium.
- The all-pack short run and default 60-minute soak eventually pass before merge.
- `make e2e-attestation-check`, frontend checks, Go checks, and risk guardrails pass.
