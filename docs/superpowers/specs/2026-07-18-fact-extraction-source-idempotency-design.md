# Fact Extraction Source-Identity Idempotency Design

## Scope

Make fact extraction replay-safe without changing outbox, Milvus search, worker reload, or profile wiring.

## Identity And Canonical Payload

An extracted fact with source provenance is identified inside one tenant schema by user, ownership scope, optional owning agent, source message ID, and the original LLM output ordinal. The ordinal is assigned before confidence filtering, sorting, and persist-limit truncation, so those operations never renumber surviving facts.

The payload hash uses `memory-fact-payload:v1:sha256:` plus SHA-256 over deterministic JSON. The payload includes identity fields defensively and the fact's content, importance, confidence, category, source, and canonical entities. Entity names are trimmed according to the existing entity-name behavior, empty names are rejected, and duplicates are removed before bytewise stable sorting. User ownership excludes agent ID; agent ownership requires and includes agent ID.

## Atomic Persistence

The application calls a narrow extracted-fact writer port. PostgreSQL inserts the fact with `ON CONFLICT DO NOTHING`, then locks and reads the canonical row. A matching hash returns that existing fact as an idempotent success. A different hash returns a typed conflict. Entity create/increment operations run in the same tenant transaction and only execute for the winning fact insert.

The winning insert returns the entity IDs produced by that transaction. A replay does not query or reconstruct entity IDs because entities are mutable and the schema has no durable fact-to-entity relation; replay therefore returns an empty `EntityIDs` list and remains independent of later entity rename or soft deletion.

Legacy facts without source identity continue through the existing repository path.

## Failure Semantics

PostgreSQL commits before embedding and Milvus upsert. Both newly created and idempotently reused facts continue through embedding and vector upsert using the persisted fact ID. A typed source conflict stops before entity or vector mutation.

## Schema

`memory_facts` gains nullable `source_message_id`, `source_task_id`, `source_ordinal`, and `source_payload_hash`. Separate partial unique indexes enforce user and agent ownership keys. Existing rows remain null and are not inferred or backfilled.
