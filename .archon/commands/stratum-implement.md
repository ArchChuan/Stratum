Implement one approved Stratum task only. First execute the `stratum-context` procedure and read the task's complete call chain. Follow TDD using a nearby high-quality test as the explicit template. Mock external dependencies through domain ports; do not use build tags or init replacement.

Keep handler methods limited to parsing, tenant extraction, application calls, and rendering. Keep business invariants in domain methods, orchestration in application, and IO/error translation in infrastructure. For tenant DDL, preserve historical tenants with ordered idempotent backfill before dependent indexes or constraints.

Run the narrow failing test, implement the minimum change, rerun it, then run `.archon/scripts/verify-short.sh`. Do not merge, touch unrelated files, weaken correct tests, or claim E2E completion.
