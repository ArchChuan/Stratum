# ClawHermes-AI-Go Project Rules
## Andrej Karpathy Core Twelve Mandatory Principles
Global Default Principle: Caution over speed. Prefer correctness, clarity, and safety over velocity. All tasks must follow these 12 rules unconditionally.

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
Rule 6: Strict Token Budget — No Silent Overrun
Incident: Long debugging sessions looped in outdated context, repeating invalid solutions due to token saturation.
Rule: Single task max 4k tokens, single session max 30k tokens. When approaching limits, actively summarize, checkpoint, and reset context. Never silently exceed budget.
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
Karpathy Core Four Meta Principle (Overall Execution Order)
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

## Go & Agent Engineering Standards
- Follow Go idiomatic style, goroutine-safe, no memory leak.
- Single-responsibility modules, clean dependency boundaries.
- Use Zap structured logging only; no fmt print.
- Milvus logic only in pkg/milvus; NATS only in pkg/nats.
- Strict MCP & Skill-Plugin extensibility for agent workflow.

## Output Constraints (Critical for Token Save)
- ONLY output runnable code, config or schema.
- NO verbose explanation, NO redundant text, NO off-topic content.
- Surgical changes only: edit required scope, no unrelated refactoring.

## Validation & Safety
- Run `go vet` and `go test -short` after every change.
- Do NOT modify config/prod.yaml, internal/auth/* without explicit approval.
- No destructive operations without confirmation.

---

## Enterprise-Grade Development Standards

### Common CLI Operations & Commands
- All terminal operations must be scripted and idempotent for production use
- Use shell scripts for deployment, testing, and maintenance tasks
- Document all custom commands in project README with examples
- Avoid manual steps in critical paths; automate everything repeatable
- Maintain command version compatibility; breaking changes require migration plan
- Log all CLI command execution with timestamp, user, and exit code

### Code Style & Standards
- Follow Google Go style guide strictly; use `gofmt` and `golangci-lint` for enforcement
- Consistent naming: PascalCase for exported, camelCase for unexported identifiers
- Maximum line length: 120 characters; break long lines strategically
- Import organization: std lib → third-party → internal (use `goimports` tool)
- No commented-out code; delete and use git history for recovery
- All public functions require comments explaining purpose and parameters
- Cyclomatic complexity: max 10 per function (measure with `gocyclo`)

### UI & Content Design Guidelines
- All UI components must meet WCAG 2.1 AA accessibility standards (automatic testing required)
- Use semantic HTML5 and ARIA labels; no div-spam for layout
- Responsive design mandatory: test on desktop, tablet, mobile viewports
- Document all design tokens: colors, spacing, typography, transitions
- Maintain component API documentation; breaking changes require deprecation period
- Dark mode and high-contrast mode support mandatory
- Color blindness testing: use tools like Coblis or Color Oracle

### Frontend Components & Interaction Patterns
- Component prop interfaces must be TypeScript typed and JSDoc documented
- All interactive elements require full keyboard navigation support (Tab, Enter, Escape, Arrow keys)
- Consistent loading states: skeleton screens, spinners, or progress indicators
- Consistent error states: user-friendly messages, error boundaries, retry mechanisms
- Use controlled components for forms; implement proper input validation
- Implement debouncing/throttling for high-frequency events (scroll, resize, input)
- Maximum render time: 16ms for 60fps (use React DevTools Profiler)

### State Management
- Centralize state; avoid prop drilling beyond 3 levels (use context or global state)
- Immutable state updates enforced: leverage immer, Zustand, or Redux patterns
- Implement transaction-like patterns for multi-step operations with rollback
- Clear ownership: each module owns, initializes, and invalidates its state slice
- State persistence: serialize critical state to localStorage with version stamping
- State versioning: handle backwards compatibility for persisted state
- Implement state debugging: time-travel debugging for development, state snapshots for monitoring

### Logging Standards
- Use Zap for structured logging exclusively (no fmt.Print, no printf)
- Log levels strictly enforced:
  - DEBUG: development details, variable values, function entry/exit
  - INFO: operational events, service startup, requests
  - WARN: recoverable issues, retries, degraded operations
  - ERROR: failures, exceptions, operational errors
  - FATAL: system crashes, unrecoverable errors (shutdown imminent)
- Include context fields: request_id, user_id, tenant_id, operation, timestamp, service_version
- Sensitive data policy: never log passwords, tokens, PII, API keys (use masking if necessary)
- Log sampling for high-volume operations (sample rates configurable)
- Structured log output: JSON format for production parsing
- Log retention: minimum 30 days, configurable per environment

### Error Handling Patterns
- Distinguish error types: system errors (500), client errors (400), not found (404)
- Wrap errors with context: `fmt.Errorf("operation_name: %w", err)` for trace chains
- Implement exponential backoff with jitter for transient failures: base 100ms, max 10s
- Circuit breaker pattern for external dependencies: fail fast after N consecutive failures
- Graceful degradation: provide partial functionality when dependencies fail
- Error budget: track error rates; alert when budget exhausted
- Actionable error messages for clients: include what failed, why, and recovery steps
- Error metrics: count by type, log latency for errors
- Custom error types for domain-specific failures (not just string errors)

### Feature Flags (Gradual Rollout & Experimentation)
- All experimental features must use feature flags in centralized config store
- Flag storage: Redis or database with sub-second evaluation latency
- Lazy-load and cache flags: refresh every 5-30 seconds
- Flag evaluation must not block critical path: fail open (default to safe state)
- Document flag lifecycle: owner, activation date, expiration date, rollback procedure
- Support flag targeting: by user, tenant, percentage, custom attributes
- Flag monitoring: track adoption, performance impact, error rate changes
- Clean up: remove flags 30 days after full rollout (add to backlog explicitly)

### Debugging Guide
- Enable debug logging: set `LOG_LEVEL=debug` or `GODEBUG=...` environment variables
- Use structured log queries to trace request flow across all services
- Profiling (staging/development only):
  - CPU profiling: `go tool pprof http://localhost:6060/debug/pprof/profile`
  - Memory profiling: track allocations, goroutine leaks
  - Trace: `go tool trace` for execution timeline analysis
- Distributed tracing: propagate trace IDs across service calls using OpenTelemetry
- Transaction tracing: end-to-end trace from client request to database query
- Breakpoint debugging: use Delve (`dlv`) for step-through debugging in development
- Log aggregation: centralize logs in ELK, Datadog, or Cloud Logging

### FAQ & Troubleshooting
- **Memory leaks in goroutines**: Always cancel contexts, close channels, use defer for cleanup
  ```go
  defer cancel()
  defer close(ch)
  ```
- **Race conditions in tests**: Run `go test -race` before every commit; fix races immediately
- **Flaky integration tests**: Isolate test data, use deterministic seeding, increase timeouts for CI
- **Slow database queries**: Enable query logging, use EXPLAIN ANALYZE, index frequently filtered columns
- **High latency in APIs**: Profile CPU/memory, check goroutine count, trace slow requests
- **Connection pool exhaustion**: Monitor active connections, implement connection pooling, set max connections
- **Out of memory errors**: Profile heap, check for leaks, tune garbage collection

### Pull Request Standards (Enterprise-Grade Code Review)
- PR title format: `[type](scope): description` (e.g., `[feat](auth): add OIDC support`)
  - Types: feat, fix, refactor, perf, test, docs, chore, ci
- Description must include:
  - **What**: clear description of changes
  - **Why**: business justification, issue links, design decisions
  - **How to test**: step-by-step reproduction, test commands
  - **Breaking changes**: explicitly noted, migration guide provided
  - **Performance impact**: before/after metrics if applicable
  - **Security impact**: any new permissions, data access, or vulnerabilities considered
- Self-review: comment on your own code, point out concerns
- Require approval from code owner (CODEOWNERS file)
- Require passing CI checks: tests, linting, security scan, build
- Squash commits into logical units (one feature = one commit)
- No merges while CI pipeline is failing or tests are flaky
- Minimum review duration: 24 hours for critical code, 4 hours for others
- Archive branch after merge: keep history for 7 days then delete

### Code Review Process
- Reviewers focus on: correctness, security, maintainability, performance, testing
- Automated checks first: linting, type checking, test coverage (no manual review of style issues)
- Review comments must be specific, actionable, and kind (assume good intent)
- Approve changes when satisfied; request changes only for critical issues
- Author must address every comment; re-request review after updates
- Consider performance implications: check for N+1 queries, memory allocations, CPU spikes
- Security review: input validation, SQL injection, authentication, authorization, secrets

### Testing & Coverage Requirements
- Unit test coverage minimum: 80% of logic code (exclude generated code, main functions)
- Integration tests: cover critical paths, external dependencies, error scenarios
- Test command: `go test -v -race -timeout 30s ./...`
- Benchmark critical paths: `go test -bench=. -benchmem ./internal/core`
- Table-driven tests for exhaustive scenarios
- Mock external dependencies: use interfaces for testability
- Test data: seed deterministically, clean up after each test
- Flaky test reporting: log and track; fix within 1 sprint

### Documentation & ADRs (Architecture Decision Records)
- README: project overview, quick start, architecture diagram, contribution guide
- API documentation: endpoints, request/response schemas, error codes (use OpenAPI/Swagger)
- Architecture Decision Records (ADRs): stored in `docs/adr/` with decision rationale
- Inline comments: explain WHY, not WHAT (code explains what)
- Deprecation notices: 2-sprint notice before removing APIs, maintain for 1 sprint
- Runbook for oncall: how to debug, escalate, and recover from failures
- Changelog: maintain CHANGELOG.md with user-facing changes per release

### Dependency Management
- Lock all dependencies with exact versions: use go.mod (no `@latest`)
- Regular dependency updates: weekly scan, monthly updates, quarterly major versions
- Security vulnerability scanning: Dependabot, Snyk, or Go's `go list -json -m all`
- Vendor directories: commit vendor/ for reproducible builds (or use module proxy)
- Breaking changes in dependencies: plan migration, add to sprint planning
- Licenses: track all dependencies; ensure compatible with project license (Apache 2.0 preferred)

### Monitoring & Alerting
- Metrics collection: Prometheus-compatible format (counters, gauges, histograms)
- Key metrics: request latency (p50, p95, p99), error rate, throughput, resource utilization
- Dashboards: real-time view of service health, key business metrics
- Alerts: PagerDuty or similar; alert on anomalies, not absolute thresholds
- SLOs: define service level objectives (99.9% uptime, 100ms p99 latency)
- Incident response: document runbook, post-mortem within 48 hours
- Log aggregation: centralize logs for searchability (ELK, Loki, Cloud Logging)

### Secrets & Security Management
- Store secrets in HashiCorp Vault or AWS Secrets Manager (never in git or env files)
- Rotation: rotate keys monthly (database passwords, API tokens, encryption keys)
- Least privilege: grant minimum necessary permissions to services
- Audit logging: log all secret access and modifications
- Secret scanning: use git-secrets or GitGuardian in pre-commit hooks
- Code scanning: Sonarqube or CodeQL for security vulnerabilities
- Encryption: TLS 1.2+ for transit, AES-256 for data at rest

### Release & Deployment Process
- Version tagging: semantic versioning (MAJOR.MINOR.PATCH)
- Release checklist: test plan, changelog, deployment steps, rollback plan
- Canary deployment: gradual rollout starting with 5% traffic
- Blue-green deployment: zero-downtime switching between versions
- Health checks: service must pass health probe before marking as ready
- Rollback strategy: keep previous 3 versions available for quick rollback
- Release notes: user-facing summary of features, fixes, deprecations
- Communication: notify stakeholders of deployment schedule and expected impact

---

## Development Guidelines & Best Practices

### Common CLI Operations & Commands
- All terminal operations must be scripted and idempotent when possible
- Use shell scripts for repetitive deployment and testing tasks
- Document all custom commands in project README
- Avoid ad-hoc manual steps in production pipelines

### Code Style & Standards
- Follow Google Go style guide and internal conventions strictly
- Use consistent naming: CamelCase for exported, snake_case for unexported
- Maximum line length: 120 characters
- Organize imports: std lib → third-party → internal
- No commented-out code; use git history instead

### UI & Content Design Guidelines
- Ensure all UI components follow accessibility standards (WCAG 2.1 AA)
- Use semantic HTML and ARIA labels where appropriate
- Maintain responsive design for mobile-first approach
- Document all design tokens and component APIs

### Frontend Components & Interaction Patterns
- Component prop interfaces must be fully documented
- All interactive elements require keyboard navigation support
- Implement consistent loading and error states across all components
- Use controlled components for form inputs

### State Management
- Centralize application state; avoid prop drilling beyond 2 levels
- Immutable state updates only; never mutate directly
- Use transaction-like patterns for multi-step state changes
- Clear state ownership: module responsible for initializing and invalidating its slice

### Logging Standards
- Use Zap for structured logging exclusively (no fmt.Print)
- Log levels: DEBUG (dev details) → INFO (progress) → WARN (recoverable) → ERROR (failures) → FATAL (crashes)
- Include context fields: request_id, user_id, tenant_id, operation
- Never log sensitive data (passwords, tokens, PII)

### Error Handling Patterns
- Distinguish between system errors (log + retry) and client errors (return 4xx)
- Wrap errors with context: use `fmt.Errorf("operation: %w", err)`
- Implement exponential backoff for transient failures
- Always provide actionable error messages to callers

### Feature Flags
- All experimental or gradual-rollout features must use feature flags
- Store flags in a centralized config store (Redis/DB); lazy-load and cache
- Flag evaluation must not block critical path (fail open)
- Document flag lifecycle: who owns it, activation date, removal date

### Debugging Guide
- Enable debug logging: set `LOG_LEVEL=debug` environment variable
- Use structured log queries to trace request flow across services
- Profiling: use pprof for CPU/memory analysis in staging only
- Trace transaction IDs end-to-end through all service logs

### FAQ & Troubleshooting
- **Common Issue**: Memory leaks in goroutines → Always cancel contexts, close channels
- **Common Issue**: Race conditions in tests → Run `go test -race` before commit
- **Common Issue**: Flaky integration tests → Isolate test data, use deterministic seeding
- **Common Issue**: Slow database queries → Index common filters, use EXPLAIN ANALYZE

### Pull Request Standards
- PR title: concise, imperative mood (e.g., "Add tenant isolation to auth", not "Updates auth")
- Description must include: what, why, how-to-test, any breaking changes
- Linked issues/tickets in description
- Self-review changes before requesting review
- Require approval from code owner before merge
- Run full test suite locally before pushing: `go test ./...`
- No merges while CI pipeline is failing