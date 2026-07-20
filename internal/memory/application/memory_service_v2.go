package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// CreateUserMemoryRequest creates a user-owned fact. Tenant and user IDs must come from auth context.
type CreateUserMemoryRequest struct {
	TenantID   string
	UserID     string
	Content    string
	Importance float64
}

// GetUserMemoryRequest reads a fact only when it belongs to the authenticated user.
type GetUserMemoryRequest struct {
	TenantID string
	UserID   string
	FactID   string
}

// CreateUserMemory persists a user-scoped canonical memory fact.
func (s *MemoryService) CreateUserMemory(ctx context.Context, req *CreateUserMemoryRequest) (*UserMemory, error) {
	fact, err := domain.NewFactWithMeta(req.TenantID, req.UserID, "", "",
		string(domain.ScopeUser), req.Content, req.Importance, 1.0, "other", domain.FactSourceExplicitUser, nil)
	if err != nil {
		return nil, err
	}
	if err := s.factRepo.Create(ctx, req.TenantID, fact); err != nil {
		return nil, fmt.Errorf("create user memory: %w", err)
	}
	return userMemoryFromFact(fact), nil
}

// GetUserMemory returns a canonical fact after enforcing user ownership.
func (s *MemoryService) GetUserMemory(ctx context.Context, req *GetUserMemoryRequest) (*UserMemory, error) {
	fact, err := s.factRepo.GetByID(ctx, req.TenantID, req.FactID)
	if err != nil {
		return nil, fmt.Errorf("get user memory: %w", err)
	}
	if fact.UserID != req.UserID || fact.Scope != domain.ScopeUser {
		return nil, domain.ErrScopeMismatch
	}
	return userMemoryFromFact(fact), nil
}

// ForgetUserMemory deletes a canonical fact after enforcing user ownership.
func (s *MemoryService) ForgetUserMemory(ctx context.Context, req *ForgetMemoryRequest) error {
	fact, err := s.factRepo.GetByID(ctx, req.TenantID, req.FactID)
	if err != nil {
		return fmt.Errorf("get user memory for deletion: %w", err)
	}
	if fact.UserID != req.UserID || fact.Scope != domain.ScopeUser {
		return domain.ErrScopeMismatch
	}
	return s.ForgetMemory(ctx, req)
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

// ForgetMemoryRequest requests deletion of a single fact by ID.
type ForgetMemoryRequest struct {
	TenantID string
	UserID   string
	FactID   string
}

// ForgetMemory deletes a single fact by ID.
func (s *MemoryService) ForgetMemory(ctx context.Context, req *ForgetMemoryRequest) error {
	if s.vectorStore != nil {
		collectionName := fmt.Sprintf("memory_facts_%s", strings.ReplaceAll(req.TenantID, "-", "_"))
		if err := s.vectorStore.Delete(ctx, collectionName, []string{req.FactID}); err != nil {
			return fmt.Errorf("forget memory vector replica: %w", err)
		}
	}
	if err := s.factRepo.Delete(ctx, req.TenantID, req.FactID); err != nil {
		return err
	}
	return nil
}
