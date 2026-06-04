# ClawHermes-AI-Go Project Rules

## Andrej Karpathy Core Twelve Mandatory Principles

Global Default Principle: Caution over speed. Prefer correctness, clarity, and safety over velocity. All tasks must follow these 12 rules unconditionally
---

Rule 1: Think Before Coding — No Silent Assumptions
Incident: AI silently assumed ambiguous requirements and implemented risky over-engineered logic without confirmation.
Rule: Explicitly state all assumptions before coding. If ambiguous, list multiple interpretations and ask. Never guess silently. Stop immediately when confused.
Rule 2: Simplicity First — No Speculative Over-Engineering
Incident: AI added unnecessary design patterns and future-proof abstractions for simple one-off logic.
Rule: Build the minimal correct solution only. No speculative flexibility, no unused abstractions, no premature optimization. If simpler code exists, rewrite it.
Rule 3: Surgical Changes — Minimize Change Surface
Incident: AI fixed one bug but silently refactored neighboring code, causing new regressions.
Rule: Only modify code directly related to the task. No unrelated refactoring, cleaning, renaming, or styling changes. Match existing project style strictly.
Rule 4: Goal-Driven Execution — Verify Before Success Claim
Incident: AI claimed fixes were complete without verification, leading to production failures.
Rule: Define clear success criteria before implementation. Complete verification and testing before marking done. Never submit untested code.
Rule 5: Do Not Assign Non-Linguistic Work to AI Models
Incident: Claude judged 503 retrys logic via natural language reasoning, breaking strict technical retry policies.
Rule: AI handles only language tasks: classification, summarization, reasoning. All routing, retry, state machine, and technical control logic must be hard-coded.
Rule 6: Strict Token Budget — Automatic Continuation
Incident: Long debugging sessions looped in outdated context, repeating invalid solutions due to token saturation.
Rule: Single task max 4k tokens, single session max 30k tokens.
When approaching token limits, the AI will AUTOMATICALLY:

1. Summarize completed work
2. Create a checkpoint
3. Reset context to minimal necessary
4. CONTINUE executing the task WITHOUT human intervention
The AI will only pause and ask for confirmation if the session exceeds 28k tokens (95% of max).
No silent overrun, no unnecessary stops.
Rule 7: Resolve Conflicts Explicitly — No Compromise
Incident: AI merged two conflicting error-handling logics, creating hybrid broken code that swallowed errors twice.
Rule: Facing conflicting implementations, choose one valid approach, mark the other for removal. Never write ambiguous, hybrid, compromise code.
Rule 8: Read Fully Before Writing
Incident: AI duplicated existing stable functions and overwrote production interfaces due to incomplete code reading.
Rule: Read all related files, interfaces, and call chains before writing new code. No code change without full context awareness. Nothing is “unrelated” by appearance.
Rule 9: Validate Business Intent, Not Surface Output
Incident: Tests passed superficially but core business logic failed in production.
Rule: Tests must verify business correctness, not just return values. Passing tests that do not validate intent are invalid. Fail fast on logic deviation.
Rule 10: Long Operations Must Have Checkpoints
Incident: Multi-step refactoring deviated at step 4, and AI continued building on wrong foundations, causing massive rollback cost.
Rule: Every complex step requires a checkpoint: record done work, verified result, and remaining tasks. Stop immediately if any step fails validation.
Rule 11: Project Convention Over Personal Taste
Incident: AI introduced new technical patterns inconsistent with project standards, breaking CI and lifecycle logic.
Rule: Maintain internal project consistency absolutely. Follow existing architecture, patterns, and style. No personal preference or unauthorized pattern replacement.
Rule 12: Expose Errors — Never Cover Up
Incident: AI reported full migration success while silently skipping data records, causing delayed business reconciliation failure.
Rule: Any skip, uncertainty, or partial failure must be explicitly declared. Default to expose risks instead of hiding them. No silent failure tolerance.

---

### Karpathy Core Four Meta Principle (Overall Execution Order)

1. Make it work → 2. Make it right → 3. Make it fast → 4. Make it scalable## Layered Context Index
This file is Layer 1 (High-frequency behavioral rules).
Detailed project facts and task-specific rules are in the following standardized files:

### Layer 2 - Project Facts

(Directory structure, dependency versions, error handling, concurrency rules)

- Documentation: [`docs/agent/project.md`](docs/agent/project.md)

### Layer 3 - Task-Specific Rules

(Mandatory reading before modifying corresponding modules)

- Milvus vector database: [`docs/agent/milvus.md`](docs/agent/milvus.md)
- NATS/Hermes event bus: [`docs/agent/nats.md`](docs/agent/nats.md)
- API endpoints & Gateway: [`docs/agent/api.md`](docs/agent/api.md)
- Agent core system: [`docs/agent/agent.md`](docs/agent/agent.md)
- Observability & monitoring: [`docs/agent/observability.md`](docs/agent/observability.md)

## Go Coding Standards

- Go idioms + goroutine-safe + Zap logging (no fmt.Print)
- Single responsibility, clean boundaries; Milvus → pkg/milvus, NATS → pkg/nats
- Line length ≤ 120 chars; imports: stdlib → third-party → internal
- All public functions need comments; cyclomatic complexity ≤ 10

## Validation

- After each change: `go vet && go test -short ./...`
- Never modify: config/prod.yaml, internal/auth/*
- No untested code committed

## Testing

- Coverage ≥ 80% logic code; integration tests verify critical paths
- Table-driven tests; mock external dependencies
- `go test -v -race -timeout 30s ./...`

## PR Standards

- Format: `[type](scope): description` (feat/fix/refactor/perf/test/docs/chore/ci)
- Include What/Why/HowToTest; mark Breaking Changes
- Pass CI (lint/test/scan); code owner approval; min 4h review

## Logging & Monitoring

- Zap structured logs only (DEBUG/INFO/WARN/ERROR/FATAL)
- Include context: request_id/user_id/tenant_id/operation/timestamp
- Never log passwords/tokens/PII/API keys
- Key metrics: latency (p50/95/99) + error rate + throughput

## Security

- Store secrets in Vault or AWS Secrets Manager (never in git)
- Monthly key rotation
- Least privilege; audit all secret access
- TLS 1.2+ transit, AES-256 at-rest
- Pre-commit: git-secrets/GitGuardian

## Error Handling

- Wrap errors with context: `fmt.Errorf("operation: %w", err)`
- Transient failures: exponential backoff (base 100ms, max 10s)
- External dependencies: circuit breaker pattern
- Custom error types (never plain strings)
