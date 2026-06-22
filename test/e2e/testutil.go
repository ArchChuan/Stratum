package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/persistence"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// MemoryTestEnv holds resources for E2E memory tests.
type MemoryTestEnv struct {
	PGPool        *pgxpool.Pool
	Redis         *redis.Client
	MemoryService *application.MemoryService
	FactRepo      port.FactRepo
	EntityRepo    port.EntityRepo
	Queue         port.ExtractionQueue
	TenantID      string
	UserID        string
	AgentID       string
}

// SetupMemoryTestEnv creates isolated tenant schema + mocked dependencies.
func SetupMemoryTestEnv(t *testing.T) *MemoryTestEnv {
	t.Helper()

	ctx := context.Background()

	// Step 1: Connect to PostgreSQL (skip if unavailable)
	pool, err := pgxpool.New(ctx, "postgres://postgres:postgres@localhost:5432/stratum_test?sslmode=disable")
	if err != nil {
		t.Skipf("PostgreSQL unavailable: %v", err)
	}

	// Step 2: Generate unique tenant ID for isolation
	tenantID := fmt.Sprintf("test_%d", time.Now().UnixNano())
	schemaName := fmt.Sprintf("tenant_%s", tenantID)

	// Step 3: Create isolated tenant schema
	_, err = pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schemaName))
	require.NoError(t, err, "create tenant schema")

	_, err = pool.Exec(ctx, fmt.Sprintf("SET search_path = %s, public", schemaName))
	require.NoError(t, err, "set search_path")

	// Step 4: Apply tenant DDL (memory_facts, memory_entities, memory_extraction_queue)
	ddl := `
		CREATE TABLE IF NOT EXISTS memory_facts (
		    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		    user_id         TEXT NOT NULL,
		    agent_id        TEXT,
		    scope           TEXT NOT NULL CHECK (scope IN ('user', 'agent')),
		    content         TEXT NOT NULL,
		    importance      FLOAT8 NOT NULL DEFAULT 0.5 CHECK (importance BETWEEN 0 AND 1),
		    frecency_score  FLOAT8 NOT NULL DEFAULT 0,
		    access_count    INT NOT NULL DEFAULT 0,
		    last_accessed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		    superseded_by   UUID,
		    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'superseded', 'archived', 'deleted')),
		    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		    deleted_at      TIMESTAMPTZ
		);
		CREATE INDEX IF NOT EXISTS idx_memory_facts_user_scope ON memory_facts (user_id, scope, status);
		CREATE INDEX IF NOT EXISTS idx_memory_facts_frecency ON memory_facts (frecency_score DESC) WHERE status = 'active';

		CREATE TABLE IF NOT EXISTS memory_entities (
		    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		    user_id                 TEXT NOT NULL,
		    agent_id                TEXT,
		    scope                   TEXT NOT NULL CHECK (scope IN ('user', 'agent')),
		    name                    TEXT NOT NULL,
		    entity_type             TEXT NOT NULL,
		    profile                 TEXT NOT NULL DEFAULT '',
		    fact_count              INT NOT NULL DEFAULT 0,
		    fact_count_since_rebuild INT NOT NULL DEFAULT 0,
		    last_seen_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		    last_profile_rebuild_at TIMESTAMPTZ,
		    status                  TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deleted')),
		    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_memory_entities_user_scope ON memory_entities (user_id, scope, status);

		CREATE TABLE IF NOT EXISTS memory_extraction_queue (
		    id          BIGSERIAL PRIMARY KEY,
		    message_id  TEXT NOT NULL,
		    user_id     TEXT NOT NULL,
		    agent_id    TEXT,
		    content     TEXT NOT NULL,
		    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
		    retry_count INT NOT NULL DEFAULT 0,
		    error_msg   TEXT,
		    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`
	_, err = pool.Exec(ctx, ddl)
	require.NoError(t, err, "apply tenant DDL")

	// Step 5: Connect to Redis (skip if unavailable)
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   0,
	})
	if err := redisClient.Ping(ctx).Err(); err != nil {
		pool.Close()
		t.Skipf("Redis unavailable: %v", err)
	}

	// Step 6: Build MemoryService with mocked LLM/Vector/Embed
	memoryService, factRepo, entityRepo, queue := newMemoryService(pool, redisClient)

	env := &MemoryTestEnv{
		PGPool:        pool,
		Redis:         redisClient,
		MemoryService: memoryService,
		FactRepo:      factRepo,
		EntityRepo:    entityRepo,
		Queue:         queue,
		TenantID:      tenantID,
		UserID:        "test-user-001",
		AgentID:       "test-agent-001",
	}

	// Step 7: Register cleanup
	t.Cleanup(func() {
		ctx := context.Background()
		// Drop schema CASCADE removes all tables
		_, _ = pool.Exec(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName))
		// Clear Redis buffer keys
		_ = redisClient.FlushDB(ctx).Err()
		pool.Close()
		redisClient.Close()
	})

	return env
}

// newMemoryService constructs MemoryService with mocked LLM extractor.
func newMemoryService(pool *pgxpool.Pool, redis *redis.Client) (*application.MemoryService, port.FactRepo, port.EntityRepo, port.ExtractionQueue) {
	factRepo := persistence.NewFactRepo(pool)
	entityRepo := persistence.NewEntityRepo(pool)
	queue := persistence.NewExtractionQueue(pool)

	service := application.NewMemoryService(
		factRepo,
		entityRepo,
		queue,
		&mockVectorStore{},
		&mockLLMExtractor{},
		&mockEmbedClient{},
		redis,
	)

	return service, factRepo, entityRepo, queue
}

// mockLLMExtractor returns deterministic facts for testing.
type mockLLMExtractor struct{}

func (m *mockLLMExtractor) ExtractFacts(ctx context.Context, userID, agentID, message string) ([]*port.ExtractedFact, error) {
	// Deterministic extraction: if message contains "dark mode", extract preference fact
	if len(message) > 0 {
		return []*port.ExtractedFact{
			{
				Content:    "User prefers dark mode",
				Importance: 0.8,
				Entities:   []string{"dark mode"},
			},
		}, nil
	}
	return nil, nil
}

// mockEmbedClient returns deterministic embeddings.
type mockEmbedClient struct{}

func (m *mockEmbedClient) Embed(ctx context.Context, text string) ([]float32, error) {
	// Return fixed 1024-dim vector
	vec := make([]float32, 1024)
	for i := range vec {
		vec[i] = 0.1
	}
	return vec, nil
}

func (m *mockEmbedClient) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i], _ = m.Embed(ctx, texts[i])
	}
	return result, nil
}

// mockVectorStore stores vectors in memory for testing.
type mockVectorStore struct {
	docs map[string]*port.VectorDoc // collectionName_docID -> doc
}

func (m *mockVectorStore) Upsert(ctx context.Context, collectionName string, docs []*port.VectorDoc) error {
	if m.docs == nil {
		m.docs = make(map[string]*port.VectorDoc)
	}
	for _, doc := range docs {
		key := fmt.Sprintf("%s_%s", collectionName, doc.ID)
		m.docs[key] = doc
	}
	return nil
}

func (m *mockVectorStore) Search(ctx context.Context, collectionName string, queryVector []float32, topK int, filter map[string]interface{}) ([]*port.VectorDoc, error) {
	if m.docs == nil {
		return nil, nil
	}

	var hits []*port.VectorDoc
	for key, doc := range m.docs {
		if len(key) > len(collectionName) && key[:len(collectionName)] == collectionName {
			// Simple filter match (user_id)
			if filter != nil {
				if userID, ok := filter["user_id"].(string); ok {
					if docUserID, exists := doc.Metadata["user_id"].(string); exists && docUserID != userID {
						continue
					}
				}
			}
			// Return with fixed similarity score
			docCopy := *doc
			docCopy.Similarity = 0.95
			hits = append(hits, &docCopy)
		}
	}

	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

func (m *mockVectorStore) Delete(ctx context.Context, collectionName string, ids []string) error {
	if m.docs == nil {
		return nil
	}
	for _, id := range ids {
		key := fmt.Sprintf("%s_%s", collectionName, id)
		delete(m.docs, key)
	}
	return nil
}

func (m *mockVectorStore) CreateCollection(ctx context.Context, collectionName string, dimension int) error {
	return nil // no-op for in-memory mock
}
