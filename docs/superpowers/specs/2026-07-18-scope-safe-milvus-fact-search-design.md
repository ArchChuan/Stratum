# Scope-Safe Milvus Fact Search Design

## Goal

Enable real Milvus retrieval for fact-centric memory without weakening tenant,
user, or agent-scope isolation.

## Port Contract

Replace the untyped search filter map with `VectorSearchFilter`. It carries the
user owner, optional current agent, and explicit user-scope and agent-scope
flags. `Validate` returns a recognizable input error and rejects an empty user,
no enabled scopes, or agent scope without an agent ID.

`VectorDoc` exposes Milvus L2 results as `Distance`. It does not populate the
legacy `Similarity` field with a distance.

## Adapter Contract

The adapter validates before invoking Milvus. It compiles only these forms:

- current agent: `user_id == U && (scope == "user" || (scope == "agent" && agent_id == A))`
- no agent: `user_id == U && scope == "user"`

Values use Milvus string-literal escaping. Unsupported scope combinations fail
closed. Valid requests call `SearchWithFilter` and preserve result order while
mapping ID, content, source document, chunk index, and L2 distance.

## Availability Semantics

The Milvus package exposes a typed availability error. Initial TCP or gRPC
connection failures and existing-client load/search failures with gRPC
`Unavailable` or `DeadlineExceeded` are availability failures. A context
deadline is classified as availability only when the caller has not actively
canceled the request. Caller cancellation, dimensions, schema, filters, and
other Milvus errors remain ordinary errors.

`RecallMemory` validates its request before dependencies. Embedding errors and
ordinary vector errors fail. Typed Milvus availability errors degrade to the
PostgreSQL trigram branch. A missing collection remains an empty vector result.

## Verification

Unit tests cover validation, expression escaping, result mapping, error
classification, fallback, and dependency-free rejection of invalid requests.
An integration-tagged test provisions a real Milvus 2.4.15 collection with
cross-user and cross-agent fixtures, validates allowed results and ordering,
and checks a missing collection. CI runs it with `REQUIRE_MILVUS_E2E=1`, so an
unavailable service fails rather than skips.
