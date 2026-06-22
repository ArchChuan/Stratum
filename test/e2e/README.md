# Memory v2 End-to-End Tests

## Overview

E2E test suite covering full memory lifecycle with real PostgreSQL + Redis + mocked LLM/Vector components.

**Test scenarios:**

1. Message buffering → extraction → recall → forget
2. Memory context injection into Agent system prompt
3. Admin diagnostics (fact counts, queue lag, top entities)

## Architecture

- **Real dependencies**: PostgreSQL (tenant schema isolation), Redis (buffer)
- **Mocked dependencies**: LLM extractor (deterministic JSON), Vector store (in-memory), Embedder (fixed vectors)
- **Isolation**: Each test creates isolated `tenant_test_*` schema, cleaned up via `DROP SCHEMA CASCADE`
- **Execution**: Sequential (no parallel) to avoid resource contention

## Prerequisites

1. **PostgreSQL** running on `localhost:5432`:

   ```bash
   docker run -d --name postgres-test \
     -e POSTGRES_PASSWORD=postgres \
     -e POSTGRES_DB=stratum_test \
     -p 5432:5432 postgres:15
   ```

2. **Redis** running on `localhost:6379`:

   ```bash
   docker run -d --name redis-test -p 6379:6379 redis:7
   ```

3. **Go 1.22+** with test dependencies:

   ```bash
   go mod download
   ```

## Running Tests

**Single test:**

```bash
go test -v ./test/e2e/... -run TestMemoryLifecycle
```

**All E2E tests:**

```bash
go test -v ./test/e2e/...
```

**Compile check only:**

```bash
go test -c ./test/e2e/...
```

**Skip if dependencies unavailable:**
Tests automatically skip with `t.Skip()` if PostgreSQL/Redis unreachable.

## Test Structure

### testutil.go

- `SetupMemoryTestEnv(t)` - Creates isolated test environment
- `mockLLMExtractor` - Returns deterministic facts
- `mockEmbedClient` - Returns fixed 1024-dim vectors
- `mockVectorStore` - In-memory vector storage

### memory_lifecycle_test.go

Full lifecycle test:

1. Buffer 5 messages → flush trigger
2. Extract facts via mocked LLM
3. Recall memories via hybrid search
4. Forget specific fact
5. Verify fact no longer recalled

### memory_context_test.go

Context injection test:

1. Create 3 facts with entities
2. Call BuildContext with scope filter
3. Verify context text includes facts
4. Verify entity profiles included

### memory_admin_test.go

Admin diagnostics test:

1. Create facts across 2 tenants
2. Call GetDiagnostics
3. Verify fact counts, queue lag, top entities
4. Test cross-tenant forget (IAM scope check)

## Cleanup

Tests auto-cleanup on exit:

- `DROP SCHEMA tenant_* CASCADE` removes all tables
- `FLUSHDB` clears Redis buffer keys

Manual cleanup (if tests crash):

```bash
# List test schemas
psql -U postgres -d stratum_test -c "\dn tenant_test_*"

# Drop all test schemas
psql -U postgres -d stratum_test -c "DROP SCHEMA IF EXISTS tenant_test_1234 CASCADE"

# Clear Redis
redis-cli FLUSHDB
```

## CI Integration

GitHub Actions workflow (`.github/workflows/memory-e2e.yml`):

- PostgreSQL/Redis via service containers
- Timeout: 10 minutes per test, 60 minutes suite
- Runs on: PR to main, merge to main

## Debugging

**Enable SQL logging:**

```bash
PGDEBUG=1 go test -v ./test/e2e/... -run TestMemoryLifecycle
```

**Check tenant schema:**

```sql
\c stratum_test
SET search_path = tenant_test_1234, public;
\dt
SELECT * FROM memory_facts;
```

**Check Redis buffer:**

```bash
redis-cli KEYS "memory:buffer:*"
redis-cli LRANGE "memory:buffer:test_tenant:user001:agent001:conv001" 0 -1
```

## Known Limitations

1. **No Milvus**: Vector store mocked in-memory (E2E doesn't test real Milvus operations)
2. **No LLM calls**: Extractor returns deterministic facts (no network I/O)
3. **Sequential only**: Tests must run serially (shared PostgreSQL/Redis)
4. **No GC worker**: Garbage collection tested separately in unit tests

## Related Documentation

- Memory v2 SPEC: `docs/memory/memory-v2-spec.md`
- Agent integration: `docs/agent/agent-chat-flow.md`
- Migration guide: `docs/memory/memory-v1-to-v2-migration.md`
