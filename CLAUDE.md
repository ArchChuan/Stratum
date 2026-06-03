# ClawHermes-AI-Go: Karpathy 12 Rules

**Core Principle**: Correctness > Speed. All work must follow these 12 rules unconditionally.

| Rule | Principle |
|------|-----------|
| 1️⃣ Think Before Code | Explicitly state all assumptions; ask if uncertain. Never guess silently. |
| 2️⃣ Simplicity First | Build minimal viable solution only. No over-design, no speculative abstractions. |
| 3️⃣ Surgical Changes | Modify only related code. No unrelated refactoring, renaming, or style changes. |
| 4️⃣ Verify Before Done | Define success criteria. Complete verification & testing before completion claim. |
| 5️⃣ Hard-Code Control Logic | AI handles language tasks only. All routing/retry/state machine logic must be hard-coded. |
| 6️⃣ Token Budget | Single task: 4k tokens; session: 30k tokens. Auto-continue when approaching limit. |
| 7️⃣ Resolve Conflicts | Choose one valid approach, mark other for removal. No hybrid/compromise code. |
| 8️⃣ Read Fully First | Read all related files/interfaces/call chains before writing code. No exceptions. |
| 9️⃣ Validate Business Intent | Tests must verify intent, not just return values. Fail fast on logic deviation. |
| 🔟 Checkpoint Long Ops | Record completion, verification, and remaining tasks for each complex step. |
| 1️⃣1️⃣ Project Convention | Strictly follow project architecture/style. No personal preference. |
| 1️⃣2️⃣ Expose Errors | Declare any skip, uncertainty, or partial failure explicitly. No silent failure. |

**Execution Order**: Make it work → Make it right → Make it fast → Make it scalable

---

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

## References
See [`docs/agent/project.md`](docs/agent/project.md) for project facts and corresponding module docs (milvus/nats/api/agent/observability) for task-specific rules.
