package persistence_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/persistence"
	"github.com/stretchr/testify/require"
)

type deterministicExtractor struct{}

func (deterministicExtractor) ExtractFacts(context.Context, string, string, string) ([]*port.ExtractedFact, error) {
	return []*port.ExtractedFact{{Content: "User uses Go", Importance: 0.8, FactType: "skill", Entities: []string{"Go"}}}, nil
}

type deterministicEmbedder struct{}

func (deterministicEmbedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{0.1, 0.2}, nil
}
func (deterministicEmbedder) EmbedBatch(context.Context, []string) ([][]float32, error) {
	return nil, nil
}

type failOnceVectorStore struct {
	mu  sync.Mutex
	ids []string
}

func (s *failOnceVectorStore) Upsert(_ context.Context, _ string, docs []*port.VectorDoc) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids = append(s.ids, docs[0].ID)
	if len(s.ids) == 1 {
		return errors.New("milvus unavailable")
	}
	return nil
}
func (*failOnceVectorStore) Search(context.Context, string, []float32, int, port.VectorSearchFilter) ([]*port.VectorDoc, error) {
	return nil, nil
}
func (*failOnceVectorStore) Delete(context.Context, string, []string) error { return nil }
func (*failOnceVectorStore) DeleteAllByUser(context.Context, string, string) error {
	return nil
}
func (*failOnceVectorStore) DeleteAllByAgent(context.Context, string, string) error {
	return nil
}
func (*failOnceVectorStore) CreateCollection(context.Context, string, int) error { return nil }

func extractedFactWrite(t *testing.T, tenantID, userID, agentID, scope, message string, ordinal int, content, hash string, entities ...string) *port.ExtractedFactWrite {
	t.Helper()
	fact, err := domain.NewFactWithMeta(tenantID, userID, agentID, "", scope, content, 0.8, 0.9, "other", domain.FactSourceLLMExtraction, entities)
	require.NoError(t, err)
	return &port.ExtractedFactWrite{
		Fact:        fact,
		Identity:    domain.FactSourceIdentity{MessageID: message, TaskID: 42, Ordinal: ordinal},
		PayloadHash: hash,
		EntityNames: entities,
	}
}

func TestExtractedFactRepoSequentialReplayReusesFactAndEntityMutation(t *testing.T) {
	pool := NewTestTenantPool(t, "test_fact_replay")
	repo := persistence.NewFactRepo(pool)
	ctx := context.Background()

	first, created, err := repo.CreateExtracted(ctx, "test_fact_replay", extractedFactWrite(t, "test_fact_replay", "user-1", "", "user", "message-1", 0, "fact", "v1:hash", "Go"))
	require.NoError(t, err)
	require.True(t, created)
	replay, created, err := repo.CreateExtracted(ctx, "test_fact_replay", extractedFactWrite(t, "test_fact_replay", "user-1", "", "user", "message-1", 0, "fact", "v1:hash", "Go"))
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, first.ID, replay.ID)
	require.Len(t, first.EntityIDs, 1)
	require.Empty(t, replay.EntityIDs)

	var facts, entities, factCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM tenant_test_fact_replay.memory_facts`).Scan(&facts))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*), COALESCE(max(fact_count),0) FROM tenant_test_fact_replay.memory_entities`).Scan(&entities, &factCount))
	require.Equal(t, 1, facts)
	require.Equal(t, 1, entities)
	require.Equal(t, 1, factCount)
}

func TestExtractedFactRepoConcurrentReplayHasOneCanonicalMutation(t *testing.T) {
	pool := NewTestTenantPool(t, "test_fact_concurrent")
	repo := persistence.NewFactRepo(pool)
	ctx := context.Background()

	const workers = 2
	ids := make([]string, workers)
	created := make([]bool, workers)
	errs := make([]error, workers)
	writes := make([]*port.ExtractedFactWrite, workers)
	for i := range workers {
		writes[i] = extractedFactWrite(t, "test_fact_concurrent", "user-1", "", "user", "message-1", 0, "fact", "v1:hash", "Go")
	}
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result, wasCreated, err := repo.CreateExtracted(ctx, "test_fact_concurrent", writes[i])
			errs[i], created[i] = err, wasCreated
			if result != nil {
				ids[i] = result.ID
			}
		}(i)
	}
	wg.Wait()
	require.NoError(t, errors.Join(errs...))
	require.Equal(t, ids[0], ids[1])
	require.Equal(t, 1, boolCount(created))

	var factCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT fact_count FROM tenant_test_fact_concurrent.memory_entities WHERE name='Go'`).Scan(&factCount))
	require.Equal(t, 1, factCount)
}

func TestExtractedFactRepoConflictingPayloadReturnsTypedErrorWithoutEntityMutation(t *testing.T) {
	pool := NewTestTenantPool(t, "test_fact_conflict")
	repo := persistence.NewFactRepo(pool)
	ctx := context.Background()

	_, _, err := repo.CreateExtracted(ctx, "test_fact_conflict", extractedFactWrite(t, "test_fact_conflict", "user-1", "", "user", "message-1", 0, "first", "v1:first", "Go"))
	require.NoError(t, err)
	result, created, err := repo.CreateExtracted(ctx, "test_fact_conflict", extractedFactWrite(t, "test_fact_conflict", "user-1", "", "user", "message-1", 0, "different", "v1:different", "Rust"))
	require.ErrorIs(t, err, domain.ErrFactSourceConflict)
	require.Nil(t, result)
	require.False(t, created)

	var facts, entities int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM tenant_test_fact_conflict.memory_facts`).Scan(&facts))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM tenant_test_fact_conflict.memory_entities`).Scan(&entities))
	require.Equal(t, 1, facts)
	require.Equal(t, 1, entities)
}

func TestExtractedFactRepoOwnershipKeysDoNotCollide(t *testing.T) {
	pool := NewTestTenantPool(t, "test_fact_scope_keys")
	repo := persistence.NewFactRepo(pool)
	ctx := context.Background()

	writes := []*port.ExtractedFactWrite{
		extractedFactWrite(t, "test_fact_scope_keys", "user-1", "", "user", "message-1", 0, "user fact", "v1:user"),
		extractedFactWrite(t, "test_fact_scope_keys", "user-1", "agent-1", "agent", "message-1", 0, "agent one", "v1:agent-1"),
		extractedFactWrite(t, "test_fact_scope_keys", "user-1", "agent-2", "agent", "message-1", 0, "agent two", "v1:agent-2"),
	}
	ids := map[string]bool{}
	for _, write := range writes {
		fact, created, err := repo.CreateExtracted(ctx, "test_fact_scope_keys", write)
		require.NoError(t, err)
		require.True(t, created)
		ids[fact.ID] = true
	}
	require.Len(t, ids, 3)
}

func TestExtractFactsVectorFailureRetryUsesCommittedCanonicalFact(t *testing.T) {
	pool := NewTestTenantPool(t, "test_fact_vector_retry")
	factRepo := persistence.NewFactRepo(pool)
	entityRepo := persistence.NewEntityRepo(pool)
	vectors := &failOnceVectorStore{}
	service := application.NewMemoryService(factRepo, entityRepo, nil, vectors, deterministicExtractor{}, deterministicEmbedder{}, nil, nil)
	req := &port.ExtractFactsRequest{
		TenantID: "test_fact_vector_retry", UserID: "user-1", AgentID: "agent-1", Scope: "user",
		SourceMessageID: "message-1", SourceTaskID: 42, Messages: []port.MessageDTO{{Role: "user", Content: "I use Go"}},
	}

	require.ErrorContains(t, service.ExtractFacts(context.Background(), req), "upsert vector")
	var entityID string
	require.NoError(t, pool.QueryRow(context.Background(), `UPDATE tenant_test_fact_vector_retry.memory_entities
		SET name='Renamed Go', status='deleted' WHERE name='Go' RETURNING id::text`).Scan(&entityID))
	require.NoError(t, service.ExtractFacts(context.Background(), req))
	require.Len(t, vectors.ids, 2)
	require.Equal(t, vectors.ids[0], vectors.ids[1])

	var facts, entityFactCount int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM tenant_test_fact_vector_retry.memory_facts`).Scan(&facts))
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT fact_count FROM tenant_test_fact_vector_retry.memory_entities WHERE id=$1`, entityID).Scan(&entityFactCount))
	require.Equal(t, 1, facts)
	require.Equal(t, 1, entityFactCount)
}

func boolCount(values []bool) int {
	n := 0
	for _, value := range values {
		if value {
			n++
		}
	}
	return n
}
