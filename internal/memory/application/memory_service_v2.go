package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"go.uber.org/zap"
)

// LLMExtractorResolver resolves a per-tenant LLMExtractor at call time.
type LLMExtractorResolver func(ctx context.Context, tenantID string) port.LLMExtractor

// EmbedClientResolver resolves a per-tenant EmbedClient at call time.
type EmbedClientResolver func(ctx context.Context, tenantID string) port.EmbedClient

// LLMSupersederResolver resolves a per-tenant LLM supersede judge at call time.
type LLMSupersederResolver func(ctx context.Context, tenantID string) port.LLMSuperseder

// TrajectoryReflectorResolver resolves a per-tenant trajectory reflector at call time.
type TrajectoryReflectorResolver func(ctx context.Context, tenantID string) port.TrajectoryReflector

// MemoryService orchestrates fact extraction, retrieval, entity management, context building.
type MemoryService struct {
	factRepo    port.FactRepo
	entityRepo  port.EntityRepo
	memoryRepo  port.MemoryRepo
	queue       port.ExtractionQueue
	vectorStore port.VectorStore
	llmExtract  port.LLMExtractor
	embedClient port.EmbedClient
	reflector   port.TrajectoryReflector
	buffer      *MessageBuffer
	logger      *zap.Logger

	llmExtractResolver  LLMExtractorResolver
	embedClientResolver EmbedClientResolver
	reflectorResolver   TrajectoryReflectorResolver
	judge               port.LLMSuperseder
	judgeResolver       LLMSupersederResolver
}

// NewMemoryService constructs a new MemoryService with all dependencies.
func NewMemoryService(
	factRepo port.FactRepo,
	entityRepo port.EntityRepo,
	queue port.ExtractionQueue,
	vectorStore port.VectorStore,
	llmExtract port.LLMExtractor,
	embedClient port.EmbedClient,
	messageBufferStore port.MessageBufferStore,
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
		buffer:      NewMessageBuffer(messageBufferStore, queue),
		logger:      logger,
	}
}

// SetVectorStore wires a vector store for cleanup operations (called during wiring after Milvus init).
func (s *MemoryService) SetVectorStore(vs port.VectorStore) { s.vectorStore = vs }

// SetExtractionQueue wires the NATS extraction publisher used by the Redis
// buffer flush. NATS 未就绪时为 nil，flush 路径显式降级（不静默吞错）。
func (s *MemoryService) SetExtractionQueue(q port.ExtractionQueue) { s.queue = q }

// SetMemoryRepo wires the memory entry repo for bulk deletion (called during wiring).
func (s *MemoryService) SetMemoryRepo(r port.MemoryRepo) { s.memoryRepo = r }

// SetLLMExtractResolver wires a per-tenant LLM extractor resolver (used when llmExtract is nil).
func (s *MemoryService) SetLLMExtractResolver(r LLMExtractorResolver) { s.llmExtractResolver = r }

// SetEmbedClientResolver wires a per-tenant embed client resolver (used when embedClient is nil).
func (s *MemoryService) SetEmbedClientResolver(r EmbedClientResolver) { s.embedClientResolver = r }

// SetLLMSuperseder wires a singleton LLM judge for inline supersede decisions during extraction.
func (s *MemoryService) SetLLMSuperseder(j port.LLMSuperseder) { s.judge = j }

// SetLLMSupersederResolver wires a per-tenant LLM judge resolver (used when judge is nil).
// Preferred over SetLLMSuperseder in multi-tenant wiring: the LLM gateway is resolved
// per tenant, so a singleton judge would apply one tenant's model to another's facts.
func (s *MemoryService) SetLLMSupersederResolver(r LLMSupersederResolver) { s.judgeResolver = r }

// SetTrajectoryReflector wires a singleton trajectory reflector (used when resolver is nil).
func (s *MemoryService) SetTrajectoryReflector(r port.TrajectoryReflector) { s.reflector = r }

// SetTrajectoryReflectorResolver wires a per-tenant trajectory reflector resolver.
func (s *MemoryService) SetTrajectoryReflectorResolver(r TrajectoryReflectorResolver) {
	s.reflectorResolver = r
}

// currentEmbedModel 返回当前默认嵌入模型名；无可用模型时返回 ""（legacy 名兜底）。
func (s *MemoryService) currentEmbedModel(ctx context.Context, tenantID string) string {
	if s.embedClient != nil {
		return s.embedClient.Model()
	}
	if s.embedClientResolver != nil {
		if ec := s.embedClientResolver(ctx, tenantID); ec != nil {
			return ec.Model()
		}
	}
	return ""
}

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
	var cleanupErrs []error
	if _, err := s.factRepo.DeleteAllByUser(ctx, req.TenantID, req.UserID); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("clear user facts: %w", err))
	}

	if s.vectorStore != nil {
		if err := s.vectorStore.DeleteAllByUser(ctx, req.TenantID, req.UserID); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("clear user vectors: %w", err))
		}
	}

	if s.memoryRepo != nil {
		if err := s.memoryRepo.DeleteAllByUser(ctx, req.TenantID, req.UserID); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("clear user memory entries: %w", err))
		}
	}

	if s.entityRepo != nil {
		if err := s.entityRepo.DeleteAllByUser(ctx, req.TenantID, req.UserID); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("clear user entities: %w", err))
		}
	}
	if err := errors.Join(cleanupErrs...); err != nil {
		return err
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
	var cleanupErrs []error
	if _, err := s.factRepo.DeleteAllByAgent(ctx, tenantID, agentID); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("clear agent facts: %w", err))
	}
	if s.vectorStore != nil {
		if err := s.vectorStore.DeleteAllByAgent(ctx, tenantID, agentID); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("clear agent vectors: %w", err))
		}
	}
	if s.memoryRepo != nil {
		if err := s.memoryRepo.DeleteAllByAgent(ctx, tenantID, agentID); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("clear agent memory entries: %w", err))
		}
	}
	if s.entityRepo != nil {
		if err := s.entityRepo.DeleteAllByAgent(ctx, tenantID, agentID); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("clear agent entities: %w", err))
		}
	}
	if err := errors.Join(cleanupErrs...); err != nil {
		return err
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

type ExtractFactsRequest = port.ExtractFactsRequest
type MessageDTO = port.MessageDTO

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

// UserMemory is the application-layer representation exposed to user-facing adapters.
type UserMemory struct {
	ID         string
	Scope      string
	Content    string
	Importance float64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func userMemoryFromFact(fact *domain.MemoryFact) *UserMemory {
	return &UserMemory{
		ID: fact.ID, Scope: string(fact.Scope), Content: fact.Content,
		Importance: fact.Importance, CreatedAt: fact.CreatedAt, UpdatedAt: fact.UpdatedAt,
	}
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

// UserStats returns the authenticated user's active memory counts.
// memoryCount 与 ListUserMemories 的 total 同源（CountByUser 同口径）；
// entityCount 为用户级 active 实体数。
func (s *MemoryService) UserStats(ctx context.Context, tenantID, userID string) (memoryCount, entityCount int, err error) {
	memoryCount, err = s.factRepo.CountByUser(ctx, tenantID, userID)
	if err != nil {
		return 0, 0, fmt.Errorf("count user memories: %w", err)
	}
	entityCount, err = s.entityRepo.CountUserEntities(ctx, tenantID, userID)
	if err != nil {
		return 0, 0, fmt.Errorf("count user entities: %w", err)
	}
	return memoryCount, entityCount, nil
}

// UserMemoryEntity is the application-layer representation of a user's entity topic tag.
type UserMemoryEntity struct {
	ID         string
	Name       string
	EntityType string
	FactCount  int
	LastSeenAt time.Time
}

// ListUserEntitiesRequest lists the authenticated user's active user-scope entities.
type ListUserEntitiesRequest struct {
	TenantID string
	UserID   string
	Limit    int
	Offset   int
}

// ListUserEntities returns a page of the user's active entities (topic tags) plus the total.
func (s *MemoryService) ListUserEntities(ctx context.Context, req *ListUserEntitiesRequest) ([]*UserMemoryEntity, int, error) {
	entities, err := s.entityRepo.ListUserEntities(ctx, req.TenantID, req.UserID, req.Limit, req.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list user entities: %w", err)
	}
	total, err := s.entityRepo.CountUserEntities(ctx, req.TenantID, req.UserID)
	if err != nil {
		return nil, 0, fmt.Errorf("count user entities: %w", err)
	}
	result := make([]*UserMemoryEntity, 0, len(entities))
	for _, e := range entities {
		result = append(result, &UserMemoryEntity{
			ID: e.ID, Name: e.Name, EntityType: e.EntityType,
			FactCount: e.FactCount, LastSeenAt: e.LastSeenAt,
		})
	}
	return result, total, nil
}

// ListUserMemoriesRequest lists the authenticated user's active memories, newest first.
type ListUserMemoriesRequest struct {
	TenantID string
	UserID   string
	Limit    int
	Offset   int
}

// ListUserMemories returns a page of the user's active memories plus the active total
// (CountByUser 只统计 active，与列表同口径，构成分页 total)。
func (s *MemoryService) ListUserMemories(ctx context.Context, req *ListUserMemoriesRequest) ([]*UserMemory, int, error) {
	facts, err := s.factRepo.ListUserFacts(ctx, req.TenantID, req.UserID, req.Limit, req.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list user memories: %w", err)
	}
	total, err := s.factRepo.CountByUser(ctx, req.TenantID, req.UserID)
	if err != nil {
		return nil, 0, fmt.Errorf("count user memories: %w", err)
	}
	memories := make([]*UserMemory, 0, len(facts))
	for _, fact := range facts {
		memories = append(memories, userMemoryFromFact(fact))
	}
	return memories, total, nil
}
