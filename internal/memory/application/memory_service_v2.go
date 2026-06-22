package application

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/redis/go-redis/v9"
)

// MemoryService orchestrates fact extraction, retrieval, entity management, context building.
type MemoryService struct {
	factRepo    port.FactRepo
	entityRepo  port.EntityRepo
	queue       port.ExtractionQueue
	vectorStore port.VectorStore
	llmExtract  port.LLMExtractor
	embedClient port.EmbedClient
	buffer      *MessageBuffer
}

// NewMemoryService constructs a new MemoryService with all dependencies.
func NewMemoryService(
	factRepo port.FactRepo,
	entityRepo port.EntityRepo,
	queue port.ExtractionQueue,
	vectorStore port.VectorStore,
	llmExtract port.LLMExtractor,
	embedClient port.EmbedClient,
	redisClient *redis.Client,
) *MemoryService {
	return &MemoryService{
		factRepo:    factRepo,
		entityRepo:  entityRepo,
		queue:       queue,
		vectorStore: vectorStore,
		llmExtract:  llmExtract,
		embedClient: embedClient,
		buffer:      NewMessageBuffer(redisClient, queue),
	}
}

// BufferMessage accumulates messages in Redis; flushes at K=5 or T=2min.
func (s *MemoryService) BufferMessage(ctx context.Context, req *BufferMessageRequest) error {
	return s.buffer.BufferMessage(ctx, req)
}

// ExtractFacts processes batch messages, extracts facts via LLM, checks supersede, normalizes entities.
// Implementation in extraction.go

// RecallMemory performs hybrid retrieval (vector + trigram + RRF), returns top-K facts.
// Implementation in retrieval.go

// ForgetMemory marks a fact as soft-deleted, schedules async Milvus cleanup.
func (s *MemoryService) ForgetMemory(ctx context.Context, req *ForgetMemoryRequest) error {
	// Step 1: Fetch fact to verify ownership
	fact, err := s.factRepo.GetByID(ctx, req.FactID)
	if err != nil {
		return fmt.Errorf("get fact: %w", err)
	}

	if fact.UserID != req.UserID {
		return domain.ErrScopeMismatch
	}

	// Step 2: Soft delete via domain method
	fact.MarkDeleted()

	if err := s.factRepo.Update(ctx, fact); err != nil {
		return fmt.Errorf("update fact: %w", err)
	}

	// Step 3: Delete from Milvus (best-effort, eventual consistency)
	collectionName := fmt.Sprintf("memory_facts_%s", req.TenantID)
	_ = s.vectorStore.Delete(ctx, collectionName, []string{req.FactID})
	// Intentionally ignore Milvus errors - GC worker will clean up orphaned vectors

	return nil
}

// BuildContext returns frecency-ranked facts + entity profiles for prompt injection.
func (s *MemoryService) BuildContext(ctx context.Context, req *BuildContextRequest) (*BuildContextResponse, error) {
	// TODO: implement in Task 6
	return nil, nil
}

// --- DTOs ---

// BufferMessageRequest represents a single message to accumulate in Redis.
type BufferMessageRequest struct {
	TenantID       string
	UserID         string
	AgentID        string
	ConversationID string
	MessageID      string
	Role           string
	Content        string
	CreatedAt      time.Time
}

// ExtractFactsRequest represents a batch of messages for fact extraction.
type ExtractFactsRequest struct {
	TenantID       string
	UserID         string
	AgentID        string
	ConversationID string
	Messages       []MessageDTO
}

// MessageDTO represents a single message in extraction batch.
type MessageDTO struct {
	Role    string
	Content string
}

// RecallMemoryRequest represents a memory retrieval request.
type RecallMemoryRequest struct {
	TenantID string
	UserID   string
	AgentID  string
	Query    string
	TopK     int
}

// RecallMemoryResponse contains retrieved facts.
type RecallMemoryResponse struct {
	Facts []FactDTO
}

// FactDTO represents a memory fact in response payloads.
type FactDTO struct {
	ID          string
	Content     string
	Importance  float64
	Keywords    []string
	EntityNames []string
	AccessCount int
	CreatedAt   time.Time
}

// ForgetMemoryRequest requests soft-deletion of a fact.
type ForgetMemoryRequest struct {
	TenantID string
	UserID   string
	FactID   string
}

// BuildContextRequest requests frecency-ranked context injection.
type BuildContextRequest struct {
	TenantID string
	UserID   string
	AgentID  string
	Query    string
	TopK     int
}

// BuildContextResponse contains facts and entity profiles for prompt injection.
type BuildContextResponse struct {
	Facts          []FactDTO
	EntityProfiles []EntityProfileDTO
	ContextText    string
}

// EntityProfileDTO represents an entity profile for context injection.
type EntityProfileDTO struct {
	Name    string
	Type    string
	Profile string
}
