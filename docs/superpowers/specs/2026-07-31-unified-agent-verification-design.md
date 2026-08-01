# Unified Agent Verification Design

## 1. Decision

Stratum will evolve the existing `stratum-e2e-development` skill into the
single test, review, acceptance, and release-closure workflow for Claude Code,
Codex, and future coding agents. No second `Test Skill` is created. `Test
Skill` is only the short name for the evolved existing skill.

The skill orchestrates the workflow, but protected CI and signed attestation
remain the only authority that can accept a change. Local results are feedback
and diagnostics, never release authority.

## 2. Goals and Non-goals

Goals:

- Use one machine-readable verification contract for Claude Code, Codex, human
  developers, and CI.
- Select verification depth from deterministic risk rules.
- Preserve the existing unit, integration, contract, browser E2E, short, soak,
  release-soak, cleanup, and attestation practices.
- Add independent specification and code-quality review for risk-appropriate
  changes.
- Bind evidence to the commit, manifest, runner, capability results, cleanup,
  and immutable artifact digests.
- Support safe concurrent runners and build-once/promote-by-digest release
  verification.

Non-goals:

- Creating a second testing skill or central testing service in this phase.
- Moving runner, lease, cleanup, or attestation implementation into the skill.
- Treating an agent's natural-language claim or a local test result as CI
  acceptance.
- Replacing focused unit and integration tests with expensive E2E journeys.

## 3. Architecture

```text
Claude Code / Codex / human
            |
            v
stratum-e2e-development (唯一编排入口)
            |
            +--> verification manifest and risk classifier
            +--> deterministic test runner and review contracts
            +--> CI observation and attestation verification
            |
            v
repository verification kernel
  run-scope, leases, lifecycle, fixtures, URLs, cleanup, attestation v2
            |
            v
protected CI: test -> review -> build -> verify -> promote -> deploy
```

The concurrent infrastructure work in Tasks 1-10 owns run identity, leases,
shared dependency references, dynamic addresses, lifecycle, cleanup, and
attestation v2. The skill owns planning, orchestration, failure classification,
review coordination, CI observation, and the final completion report.

## 4. Risk and Verification Matrix

The effective risk is the maximum deterministic match from changed paths,
API/schema/dependency changes, security/data rules, and explicit release intent.
Agents may raise a level but cannot lower it. Classifier failure is fail-closed.

| Level | Scope | Minimum verification |
| --- | --- | --- |
| R0 | Non-executable documentation | docs and generated-file checks |
| R1 | Local logic, UI text/style, internal refactor | static, unit, build, quality ratchet |
| R2 | API, page flow, repository, ordinary state changes | R1 + integration/contract + `e2e-system-short` |
| R3 | Auth, tenancy, migrations, Agent/MCP/Memory, messaging, vectors, external dependencies, deployment | R2 + failure paths + 600-second soak + specification and quality review |
| R4 | Release candidate promotion | R3 + 3600-second release soak + digest chain + deployment and production verification |

Unit, integration, contract, browser E2E, stateful/soak, deployment, and
production verification each prove a different contract and cannot substitute
for one another.

## 5. Verification Manifest

The repository owns one machine-readable contract at `.test/verification.yaml`.
The organization-level skill understands the common schema; repositories may
add scoped extensions. The manifest records policy authority, risk rules,
levels, capabilities, required evidence, review requirements, and attestation
schema. Its digest is included in every attestation.

The manifest must require explicit capability outcomes (`passed`, `failed`,
`blocked`, or `unreconciled`). Silent skips are invalid. The existing
attestation implementation remains the evidence source; the manifest does not
create a parallel proof format.

## 6. Skill State Machine

```text
received -> scoped -> classified -> planned -> local_verified
  -> reviewed -> ci_running -> attestation_verified -> accepted
```

Failure states are explicit: `diagnosed`, `blocked`, and `incomplete`. A
failure cannot be converted to success by a retry. Retries are diagnostic only.
Flaky quarantine requires an owner, root cause, scope, and expiry; security,
tenant, migration, cleanup, and attestation tests cannot be quarantined.

`accepted` is the only state from which the skill may report completion.

## 7. Review Contract

R1 requires an independent code-quality review. R2 adds specification review
when behavior or contract changes. R3 and R4 require both specification and
code-quality reviews, and R4 also requires release-evidence review. The
implementing agent cannot approve its own change. Review results bind to the
commit and become invalid when relevant code changes.

## 8. Agent Adapters

Claude Code and Codex use thin adapters only:

1. load the existing skill and repository manifest;
2. submit task scope and diff metadata;
3. invoke the canonical verification commands;
4. consume the structured completion report.

`CLAUDE.md`, `AGENTS.md`, hooks, and Codex/Claude CI actions contain entry-point
instructions and mechanical checks, not duplicate risk matrices or test
semantics.

The canonical command surface remains repository-owned and can be introduced
compatibly:

```text
make test-verify-plan
make test-verify-local
make test-verify-ci
make test-verify-attestation
make test-verify-report
```

## 9. Evidence and Release Chain

Evidence follows the same immutable artifact through the pipeline:

```text
build attestation
  -> test attestation
  -> staging deployment attestation
  -> release-soak attestation
  -> production verification
```

The proof binds commit SHA, manifest and policy versions, runner identity,
capability outcomes, cleanup, and artifact digests using an in-toto/SLSA-style
statement. Raw logs are short-lived CI artifacts; the durable proof is the
signed, digest-bound attestation.

## 10. Delivery Phases

1. **Task 1-2:** finish run-scope primitives and atomic secure lease
   publication; pass independent specification and quality review.
2. **Task 3-7:** complete database lifecycle, fixture identity, URL propagation,
   schema-v2 evidence, and scoped runner lifecycle.
3. **Task 8:** prove the dual-runner concurrency contract, including kill,
   timeout, port collision, and dependency-start failure paths.
4. **Task 9:** evolve the existing `stratum-e2e-development` skill in place;
   add manifest, state machine, review contract, failure taxonomy, and
   completion-report validation.
5. **Task 10:** run real concurrent acceptance, 600-second soak, and 3600-second
   release soak; verify the same artifact digest through deployment.

No phase may claim completion while its required capability is skipped,
unreconciled, or missing cleanup evidence.

## 11. Evidence Basis and Boundaries

Repository evidence: `AGENTS.md`, `stratum-e2e-development/SKILL.md`,
`Makefile`, Stateful E2E workflows, attestation checks, and the concurrent
runner Task 1-10 work in progress. Obsidian inputs are useful principles but
remain read-only and provisional unless independently verified.

External references:

- Codex instructions, hooks, non-interactive execution, and GitHub Action:
  <https://learn.chatgpt.com/docs/agent-configuration/agents-md>
  <https://learn.chatgpt.com/docs/hooks>
  <https://learn.chatgpt.com/docs/github-action>
- Claude Code hooks and GitHub Actions:
  <https://docs.anthropic.com/en/docs/claude-code/hooks>
  <https://docs.anthropic.com/en/docs/claude-code/github-actions>
- in-toto Statement and SLSA Provenance:
  <https://in-toto.io/Statement/v1>
  <https://slsa.dev/spec/v1.1/provenance>
- Playwright best practices:
  <https://playwright.dev/docs/best-practices>

These sources establish adapter and evidence-pattern capabilities; they do not
prove that Stratum has implemented them. Project code, CI results, and runtime
evidence remain authoritative.
