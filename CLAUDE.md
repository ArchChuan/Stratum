# ClawHermes-AI-Go Rules

**Default**: Caution > speed. Correctness, clarity, safety over velocity.

## Karpathy 12 Rules

| # | Rule | Mandate |
|---|------|---------|
| 1 | Think Before Coding | State all assumptions. If ambiguous, list interpretations and ask. No silent guessing. |
| 2 | Simplicity First | Minimal correct solution only. No speculative abstractions, no premature optimization. |
| 3 | Surgical Changes | Modify only task-relevant code. No unrelated refactor/rename/style. Match project style. |
| 4 | Verify Before Done | Define success criteria first. No untested code submitted. |
| 5 | No AI Control Logic | AI does language tasks only. Routing/retry/state machines must be hard-coded. |
| 6 | Token Budget | Task ≤4k tokens, session ≤30k. At limit: summarize → checkpoint → reset → continue. Pause only at 28k (95%). |
| 7 | Resolve Conflicts | Pick one valid approach; remove the other. No hybrid compromise code. |
| 8 | Read Fully First | Read all related files/interfaces/call chains before writing. Nothing is "unrelated" by appearance. |
| 9 | Validate Business Intent | Tests verify business correctness, not just return values. |
| 10 | Checkpoints on Long Ops | Record done work + verified result + remaining tasks after each complex step. Stop on validation failure. |
| 11 | Project Convention | Follow existing architecture/patterns/style. No unauthorized pattern replacement. |
| 12 | Expose Errors | Declare all skips, uncertainties, partial failures. No silent failure tolerance. |

**Meta order**: Make it work → right → fast → scalable

## Layered Context

- Layer 2 (project facts): [`docs/agent/project.md`](docs/agent/project.md)
- Layer 3 (module rules): [milvus](docs/agent/milvus.md) · [nats](docs/agent/nats.md) · [api](docs/agent/api.md) · [agent](docs/agent/agent.md) · [observability](docs/agent/observability.md)

## Go Standards

- Go idioms + goroutine-safe + Zap only (no fmt.Print)
- Single responsibility; line ≤120 chars; imports: stdlib → third-party → internal
- All public functions documented; cyclomatic complexity ≤10

## Validation

```bash
go vet && go test -short ./...       # after every change
go test -v -race -timeout 30s ./...  # full suite
```

- Never modify: `config/prod.yaml`, `internal/auth/*`
- Coverage ≥80% logic; table-driven tests; mock external deps

## PR Format

`[type](scope): description` — feat/fix/refactor/perf/test/docs/chore/ci
Include What/Why/HowToTest. Pass CI (lint/test/scan). Min 4h review.

## Logging

- Zap structured (DEBUG/INFO/WARN/ERROR/FATAL)
- Context fields: request_id/user_id/tenant_id/operation/timestamp
- Never log passwords/tokens/PII/API keys

## Security

- Secrets in Vault or AWS Secrets Manager (never git)
- Monthly key rotation; least privilege; audit secret access
- TLS 1.2+ transit, AES-256 at-rest; pre-commit: git-secrets/GitGuardian

## Error Handling

- `fmt.Errorf("operation: %w", err)` — always wrap with context
- Transient: exponential backoff (base 100ms, max 10s)
- External deps: circuit breaker; custom error types (no plain strings)
