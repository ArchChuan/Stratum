# Platform Assistant Completion Audit (2026-07-27)

This audit binds the Phase 1, Phase 2, and remote-remediation completion gates
to current code, named tests, and fresh runtime evidence. `passed` means the
listed boundary was exercised. `prerequisite_missing` is intentionally not a
pass.

## Phase 1 Completion Gate

| Requirement | Authoritative code | Test / gate | Runtime evidence | Result | Boundary |
| --- | --- | --- | --- | --- | --- |
| Clean branch diff | Repository branch | `git diff origin/main --check` | Fresh check on 2026-07-27 | passed | Re-run after the final audit commit |
| Risk and Task 9 verification | `scripts/quality/risk-regression-guard.sh` | `make risk-guardrails`; `stratum-verify go-full`; race and frontend gates | All selected Go packages, 68 frontend files / 291 tests, vet, and govulncheck passed | passed | React Router audit still reports two non-called moderate advisories |
| Tenant repository isolation | `internal/agent/infrastructure/persistence`; `pkg/tenantdb` | `TestSystemAssistantTenantIsolationAndRoleScope`; `TestSystemAssistantProposalPostgresAuthorizationSecretsAndConcurrency` | Real PostgreSQL tenant A/B scenarios passed | passed | SQL inspection remains part of architecture review |
| Ordinary Agents cannot see system tools | `internal/agent/application/system_assistant_tools.go` | `TestAssembleOptionsExposesExactlyTwoToolsOnlyToSystemAssistant` | Race suite passed | passed | Proposal tool is separately admin-gated in Phase 2 |
| Membership failure performs no evidence query | `internal/agent/application/diagnostic_policy.go`; `api/wiring/system_assistant.go` | `TestSystemAssistantDiagnosticTenantAndRoleIsolation`; `TestDiagnosticScopePolicyFailsClosed` | Focused and race suites passed | passed | Unknown roles remain forbidden |
| Official no-match is a knowledge gap | `internal/agent/infrastructure/officialdocs`; `internal/agent/domain/execution_artifact.go` | `TestSystemAssistantCallbacksPreserveNoMatchAndDiagnosticGaps`; `TestBuildExecutionArtifactsPreservesOfficialDocsFailureAsEvidenceGap` | Race suite passed | passed | No model or tenant Knowledge fallback |
| Evidence channels contain no secret | `internal/agent/application/tool_result_guard.go`; safe DTOs | `TestToolResultGuardRedactsSensitiveValues`; `TestSystemAssistantProposalPostgresAuthorizationSecretsAndConcurrency` | Go E2E and browser E2E passed without leaked markers | passed | Remote verifier emits only check names and aggregate categories |
| Phase 1 UI has no resource-change entry | Phase 1 historical boundary | Phase 2 route and component tests | Current product intentionally includes the approved Phase 2 proposal entry | superseded | Not a current-state prohibition after Phase 2 approval |

## Phase 2 Completion Gate

| Requirement | Authoritative code | Test / gate | Runtime evidence | Result | Boundary |
| --- | --- | --- | --- | --- | --- |
| Fresh risk, permission, race, frontend, and E2E gates | CI and versioned scripts | `make risk-guardrails`; `make tool-permission-test`; race; lint/build; both assistant E2Es | All passed on 2026-07-27 | passed | Stateful selector named by generated instructions is absent; direct real browser suite was run |
| Proposal SQL always enters tenant context | `internal/agent/infrastructure/persistence/resource_change_proposal_repo.go` | `TestSystemAssistantProposalPostgresAuthorizationSecretsAndConcurrency` | Cross-tenant real PostgreSQL denial passed | passed | Repository methods require explicit tenant ID |
| Authorization before read, confirm, and apply | `internal/agent/application/resource_change_proposal_service.go`; HTTP RBAC | `TestResourceChangeProposalCreateAuthorizationAndInvalidPayload`; `TestResourceChangeProposalConfirmReauthorizesAndChecksBaseline`; browser member 403 | Service, handler, and browser assertions passed | passed | Role resolution failures fail closed |
| One apply under concurrency | proposal claim SQL and service | `TestSystemAssistantProposalPostgresAuthorizationSecretsAndConcurrency`; `TestResourceChangeProposalConcurrentConfirmDoesNotFinalizeActiveClaim` | Real PostgreSQL concurrency passed | passed | Restart recovery has separate tests |
| Stale and terminal states cannot move backward | domain transition table and service | `TestProposalTransitionTable`; real-service stale/expired/failed/unknown cases | Go and browser terminal-state cases passed | passed | `unknown_outcome` requires manual reconciliation |
| Rejected secret payload is never persisted | proposal validators and safe projections | `TestSystemAssistantProposalPostgresAuthorizationSecretsAndConcurrency` | Database/event/result marker scan passed | passed | Raw invalid payload is rejected before repository create |
| MCP create has no auth; update preserves credentials | `api/wiring/resource_change_proposal.go` | `TestSystemAssistantProposalRealServices/mcp_config` | Real MCP service create/update/readback passed | passed | Proposal cannot add or replace credentials |
| No delete, publish, deploy, execute, or upload operation | closed proposal schema and adapters | `TestProposalToolSchemaUsesClosedDiscriminatedPayloads`; handler contract tests | Race and tool-permission gates passed | passed | Only Agent, Skill draft, credential-free MCP, and workspace create/update exist |
| Unknown outcome cannot retry | service terminal state and review UI | `TestResourceChangeProposalRetryMarksInterruptedApplyingAsUnknown`; `ResourceChangeProposalPage` unknown test | Desktop/mobile browser terminal view has no retry/confirm control | passed | Operator reconciliation remains external |

## Remote Remediation Task 5

| Requirement | Authoritative code | Test / gate | Runtime evidence | Result | Boundary |
| --- | --- | --- | --- | --- | --- |
| Static and focused verification | remediation implementation and deployment manifests | `stratum-verify go-full`; deployment safety; focused Go; diff check | Passed locally on 2026-07-27 | passed | Final diff check must be repeated after this file |
| Race and integration verification | affected packages and real PostgreSQL harness | race command; `scripts/test-platform-assistant-e2e.sh` | Race and all real-service cases passed | passed | Opik remote readiness is checked separately |
| Real local user journeys | real backend/frontend/DB/stub harness | `scripts/test-platform-assistant-browser-e2e.sh` | Mobile and desktop passed chat, edit, reload, apply, prompt redaction, non-empty PNG capture, no-canvas rendering, and sensitive-marker scans | passed | Deterministic local provider is test-only and loopback-bound |
| Deploy and remote acceptance | `scripts/e2e/platform-assistant-remote-verify.sh` | deployment safety contract plus post-CD verifier | Main CD run `30268637949` passed; fresh post-deploy verification passed public/member, Opik, Collector, schema, administrator, and Provider checks, then reported `prerequisite_missing: admin_session` | prerequisite_missing | The configured diagnostic and Opik-correlation chain still requires a legitimate process-only administrator bearer |
| Final evidence and knowledge gate | this audit; `tmp/knowledge-deposition/` report | final status/diff and knowledge report checks | PR #140, CI, main CD, post-deploy noncredential checks, and the task-bound knowledge report passed | passed | The ignored knowledge report remains local; it does not satisfy the separate administrator-session prerequisite |

## Resource Decision

The managed assistant does not require a tenant-created MCP, Skill, or
Knowledge workspace. Its two Phase 1 tools are code-owned and its official
documentation catalog is embedded and versioned. Binding tenant resources to
the managed profile would violate the approved isolation contract. The remote
configured chain instead requires a legitimate tenant administrator session
and a tenant-owned Provider configuration; the verifier never manufactures
either prerequisite.

## Current Conclusion

The approved product design contains exactly two delivery phases. Phase 1 and
Phase 2 are implemented and covered by the gates above. There is no approved
Phase 3 in `2026-07-23-builtin-platform-assistant-design.md`.

The remaining remote result is not an implementation phase: it is an
acceptance prerequisite. Until an administrator supplies a legitimate bearer
for a tenant that owns a Provider, the configured assistant execution and its
request-ID-to-Opik correlation remain unverified and must not be reported as
passed.
