# Facts memory boundary

Phase 0 hardens the existing Facts slice only. It does not add User or History memory.

- LLM extraction keeps omitted confidence backward-compatible by using importance, while explicit `0.0` remains zero and is filtered before persistence.
- Persisted facts carry an allowlisted category, confidence in `[0,1]`, source type, and `conversation_id`. Message DTOs do not expose a safe message ID, so provenance stops at the conversation reference and never stores message payloads.
- Extraction retains facts by confidence then importance, with stable ties and a per-round cap. Explicit corrections continue through the existing supersede flow; source is provenance, not an automatic conflict winner.
- Injection reads active facts above the confidence threshold and ranks by query relevance, frecency, confidence, then importance. Category-labelled output is bounded by the centralized Facts Top-N, timeout, and character budget constants.
- Explicit user-created facts use `explicit_user` provenance and confidence `1.0`, so archival protection does not depend on their importance score.
- The existing `memory_facts_*` Milvus collection is preserved without drop/recreate. Scope uses its existing scalar field; an allowlisted, length-checked JSON payload in the existing `source_document` field persists conversation, importance, category, confidence, and source metadata without copying arbitrary keys.
- Tenant Facts DDL remains canonical in `pkg/storage/postgres/tenant_schema.sql`; migration `020` is a paired public-schema marker only.
