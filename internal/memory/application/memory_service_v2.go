package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// LLMExtractorResolver resolves a per-tenant LLMExtractor at call time.
type LLMExtractorResolver func(ctx context.Context, tenantID string) port.LLMExtractor

// EmbedClientResolver resolves a per-tenant EmbedClient at call time.
type EmbedClientResolver func(ctx context.Context, tenantID string) port.EmbedClient

// MemoryService orchestrates fact extraction, retrieval, entity management, context building.
type MemoryService struct {
	factRepo    port.FactRepo
	entityRepo  port.EntityRepo
	memoryRepo  port.MemoryRepo
	queue       port.ExtractionQueue
	vectorStore port.VectorStore
	llmExtract  port.LLMExtractor
	embedClient port.EmbedClient
	buffer      *MessageBuffer
	logger      *zap.Logger

	llmExtractResolver  LLMExtractorResolver
	embedClientResolver EmbedClientResolver
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
	logger *zap.Logger,
) *MemoryService {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &MemoryService{
		factRepo:    factRepo,
		entityRepo:  entityRepo,
		queue:       queue,
		vectorStore: vectorStore,
		llmExtract:  llmExtract,
		embedClient: embedClient,
		buffer:      NewMessageBuffer(redisClient, queue),
		logger:      logger,
	}
}

// SetVectorStore wires a vector store for cleanup operations (called during wiring after Milvus init).
func (s *MemoryService) SetVectorStore(vs port.VectorStore) { s.vectorStore = vs }

// SetMemoryRepo wires the memory entry repo for bulk deletion (called during wiring).
func (s *MemoryService) SetMemoryRepo(r port.MemoryRepo) { s.memoryRepo = r }

// SetLLMExtractResolver wires a per-tenant LLM extractor resolver (used when llmExtract is nil).
func (s *MemoryService) SetLLMExtractResolver(r LLMExtractorResolver) { s.llmExtractResolver = r }

// SetEmbedClientResolver wires a per-tenant embed client resolver (used when embedClient is nil).
func (s *MemoryService) SetEmbedClientResolver(r EmbedClientResolver) { s.embedClientResolver = r }

// BufferMessage accumulates messages in Redis; flushes at K=5 or T=2min.
func (s *MemoryService) BufferMessage(ctx context.Context, req *BufferMessageRequest) error {
	return s.buffer.BufferMessage(ctx, req)
}

// ExtractFacts processes batch messages, extracts facts via LLM, checks supersede, normalizes entities.
// Implementation in extraction.go

// RecallMemory performs hybrid retrieval (vector + trigram + RRF), returns top-K facts.
// Implementation in retrieval.go

// ClearUserMemories hard-deletes all facts, memory entries, and entities for a user.
func (s *MemoryService) ClearUserMemories(ctx context.Context, req *ClearUserMemoriesRequest) error {
	s.logger.Info("memory.clear_user",
		zap.String("tenant_id", req.TenantID),
		zap.String("user_id", req.UserID),
	)
	_, err := s.factRepo.DeleteAllByUser(ctx, req.TenantID, req.UserID)
	if err != nil {
		return fmt.Errorf("clear user memories: %w", err)
	}

	if s.vectorStore != nil {
		if err := s.vectorStore.DeleteAllByUser(ctx, req.TenantID, req.UserID); err != nil {
			s.logger.Warn("memory.clear_user: vector delete partial failure",
				zap.String("tenant_id", req.TenantID),
				zap.String("user_id", req.UserID),
				zap.Error(err),
			)
		}
	}

	if s.memoryRepo != nil {
		if err := s.memoryRepo.DeleteAllByUser(ctx, req.TenantID, req.UserID); err != nil {
			return fmt.Errorf("clear memory entries: %w", err)
		}
	}

	if err := s.entityRepo.DeleteAllByUser(ctx, req.TenantID, req.UserID); err != nil {
		return fmt.Errorf("clear entities: %w", err)
	}

	s.logger.Info("memory.clear_user: done",
		zap.String("tenant_id", req.TenantID),
		zap.String("user_id", req.UserID),
	)
	return nil
}

// ClearAgentMemories hard-deletes all facts, memory entries, and Milvus vectors for an agent.
func (s *MemoryService) ClearAgentMemories(ctx context.Context, tenantID, agentID string) error {
	s.logger.Info("memory.clear_agent",
		zap.String("tenant_id", tenantID),
		zap.String("agent_id", agentID),
	)
	factIDs, err := s.factRepo.DeleteAllByAgent(ctx, tenantID, agentID)
	if err != nil {
		return fmt.Errorf("clear agent memories: %w", err)
	}
	if len(factIDs) > 0 && s.vectorStore != nil {
		collectionName := fmt.Sprintf("memory_facts_%s", strings.ReplaceAll(tenantID, "-", "_"))
		if err := s.vectorStore.Delete(ctx, collectionName, factIDs); err != nil {
			s.logger.Warn("memory.clear_agent: vector delete partial failure",
				zap.String("tenant_id", tenantID),
				zap.String("agent_id", agentID),
				zap.Int("fact_count", len(factIDs)),
				zap.Error(err),
			)
		}
	}
	if s.memoryRepo != nil {
		if err := s.memoryRepo.DeleteAllByAgent(ctx, tenantID, agentID); err != nil {
			return fmt.Errorf("clear agent memory entries: %w", err)
		}
	}
	if s.vectorStore != nil {
		if err := s.vectorStore.DeleteAllByAgent(ctx, tenantID, agentID); err != nil {
			s.logger.Warn("memory.clear_agent: memory vector delete partial failure",
				zap.String("tenant_id", tenantID),
				zap.String("agent_id", agentID),
				zap.Error(err),
			)
		}
	}
	if err := s.entityRepo.DeleteAllByAgent(ctx, tenantID, agentID); err != nil {
		return fmt.Errorf("clear agent entities: %w", err)
	}
	s.logger.Info("memory.clear_agent: done",
		zap.String("tenant_id", tenantID),
		zap.String("agent_id", agentID),
	)
	return nil
}

// --- DTOs ---

// BufferMessageRequest represents a single message to accumulate in Redis.
type BufferMessageRequest struct {
	TenantID       string
	UserID         string
	AgentID        string
	ConversationID string
	Scope          string
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
	Scope          string
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

// ClearUserMemoriesRequest requests deletion of all facts for a user.
type ClearUserMemoriesRequest struct {
	TenantID string
	UserID   string
}

// ClearAgentMemoriesRequest requests deletion of all facts belonging to an agent.
type ClearAgentMemoriesRequest struct {
	TenantID string
	AgentID  string
}

// ForgetMemoryRequest requests deletion of a single fact by ID.
type ForgetMemoryRequest struct {
	TenantID string
	UserID   string
	FactID   string
}

// ForgetMemory deletes a single fact by ID.
func (s *MemoryService) ForgetMemory(ctx context.Context, req *ForgetMemoryRequest) error {
	return s.factRepo.Delete(ctx, req.TenantID, req.FactID)
}
