package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/persistence"
	pgstorage "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
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
	// DSN must match the real infra credentials (docker-compose: stratum/stratum).
	// Direct-connect to Postgres 5432 (NOT pgbouncer 6432): schema/extension
	// provisioning needs session-level privileges that transaction pooling breaks.
	// Override via TEST_POSTGRES_URL to point at any real Postgres.
	dsn := os.Getenv("TEST_POSTGRES_URL")
	if dsn == "" {
		dsn = "postgres://stratum:stratum@localhost:5432/stratum_test?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("PostgreSQL unavailable: %v", err)
	}
	if pingErr := pool.Ping(ctx); pingErr != nil {
		pool.Close()
		t.Skipf("PostgreSQL unavailable: %v", pingErr)
	}

	// Step 2: Generate unique tenant ID for isolation
	tenantID := fmt.Sprintf("test_%d", time.Now().UnixNano())
	schemaName := fmt.Sprintf("tenant_%s", tenantID)

	// Step 3: Provision public schema (gen_uuid_v7, pg_trgm, public.tenants, ...).
	// Idempotent — safe to call per-test; no-op if already applied to this DB.
	if err := pgstorage.ProvisionPublicSchema(ctx, pool, zap.NewNop()); err != nil {
		pool.Close()
		t.Fatalf("provision public schema: %v", err)
	}

	// Step 4: Provision the full tenant schema via the canonical DDL
	// (pkg/storage/postgres/tenant_schema.sql is the single source of truth;
	// production startup and tests apply the same file via this call).
	if err := pgstorage.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		pool.Close()
		t.Fatalf("provision tenant schema: %v", err)
	}

	// Step 5: Connect to Redis (skip if unavailable)
	redisAddr := os.Getenv("TEST_REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	redisClient := redis.NewClient(&redis.Options{
		Addr: redisAddr,
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
	messageBufferStore := persistence.NewRedisMessageBufferStore(redis)

	service := application.NewMemoryService(
		factRepo,
		entityRepo,
		queue,
		&mockVectorStore{},
		&mockLLMExtractor{},
		&mockEmbedClient{},
		messageBufferStore,
		nil,
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

func (m *mockVectorStore) DeleteAllByUser(_ context.Context, _, _ string) error { return nil }

func (m *mockVectorStore) DeleteAllByAgent(_ context.Context, _, _ string) error { return nil }

func (m *mockVectorStore) CreateCollection(ctx context.Context, collectionName string, dimension int) error {
	return nil // no-op for in-memory mock
}
