package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
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

	historyRepo        port.HistoryRepo
	activeSnapshotRepo port.ActiveSnapshotRepo
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

// SetHistoryRepo wires the history repo for user-facing summary management
// (called during wiring; summaries are otherwise only touched by workers).
func (s *MemoryService) SetHistoryRepo(r port.HistoryRepo) { s.historyRepo = r }

// SetActiveSnapshotRepo wires the active snapshot repo for user-facing
// snapshot management.
func (s *MemoryService) SetActiveSnapshotRepo(r port.ActiveSnapshotRepo) { s.activeSnapshotRepo = r }

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

// resolveEmbedClient 返回 tenant 的嵌入客户端；未配置返回 ErrMemoryEmbeddingUnavailable
// （fail-closed：编辑前必须先能嵌入，否则拒绝写数据，spec §3 第 2 步）。
func (s *MemoryService) resolveEmbedClient(ctx context.Context, tenantID string) (port.EmbedClient, error) {
	embedder := s.embedClient
	if embedder == nil && s.embedClientResolver != nil {
		embedder = s.embedClientResolver(ctx, tenantID)
	}
	if embedder == nil {
		return nil, ErrMemoryEmbeddingUnavailable
	}
	return embedder, nil
}

// factVectorMetadata 构造事实向量元数据，与提取路径 embedAndStoreFactVector 的
// key 集合保持一致（召回 filter 依赖这些 key）。
func factVectorMetadata(fact *domain.MemoryFact) map[string]interface{} {
	return map[string]interface{}{
		"user_id":         fact.UserID,
		"agent_id":        fact.AgentID,
		"conversation_id": fact.ConversationID,
		"scope":           string(fact.Scope),
		"content":         fact.Content,
		"importance":      fact.Importance,
		"category":        fact.Category,
		"confidence":      fact.Confidence,
		"source":          fact.Source,
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
	Category   string
	Confidence float64
	Source     string
	Status     string
}

func userMemoryFromFact(fact *domain.MemoryFact) *UserMemory {
	return &UserMemory{
		ID: fact.ID, Scope: string(fact.Scope), Content: fact.Content,
		Importance: fact.Importance, CreatedAt: fact.CreatedAt, UpdatedAt: fact.UpdatedAt,
		Category: fact.Category, Confidence: fact.Confidence, Source: fact.Source,
		Status: fact.Status,
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
	// Scope 标注来源（user/agent，#29：管理页展示区分）。
	Scope string
}

// UserFactDetail 事实详情（管理页展示 / 编辑返回，字段与 gen.MemoryFactResponse 对齐）。
type UserFactDetail struct {
	ID         string
	Scope      string
	Content    string
	Category   string
	Source     string
	Status     string
	Importance float64
	Confidence float64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func userFactDetailFromFact(fact *domain.MemoryFact) *UserFactDetail {
	return &UserFactDetail{
		ID: fact.ID, Scope: string(fact.Scope), Content: fact.Content,
		Category: fact.Category, Source: fact.Source, Status: fact.Status,
		Importance: fact.Importance, Confidence: fact.Confidence,
		CreatedAt: fact.CreatedAt, UpdatedAt: fact.UpdatedAt,
	}
}

// ListUserFactsFilteredRequest 事实列表查询（GET /memory/facts）。
type ListUserFactsFilteredRequest struct {
	TenantID      string
	UserID        string
	Query         string
	ImportanceMin *float64
	ImportanceMax *float64
	Category      string
	Limit         int
	Offset        int
}

// UpdateUserFactPatch 事实编辑补丁，至少一项（PATCH /memory/facts/:id）。
type UpdateUserFactPatch struct {
	Content    *string
	Importance *float64
	Category   *string
}

// UserSummary 历史摘要条目（管理页只读 + 删除）。
type UserSummary struct {
	ID             string
	Summary        string
	Tier           string
	ConversationID string
	Importance     float64
	PeriodEnd      time.Time
	CreatedAt      time.Time
	// Scope 标注来源（user/agent，#29：管理页展示区分）。
	Scope string
}

// UserSnapshot 活跃快照（每 (user_id, agent_id) 一条，管理页展示/编辑/清空）。
type UserSnapshot struct {
	AgentID          string
	AgentName        string
	ConversationName string
	WorkContext      []string
	PersonalContext  []string
	TopOfMind        []string
	ExpiresAt        time.Time
	UpdatedAt        time.Time
	Status           string
}

// UpdateUserSnapshotPatch 快照编辑（三段数组整体替换）。
type UpdateUserSnapshotPatch struct {
	WorkContext     []string
	PersonalContext []string
	TopOfMind       []string
}

// UserEntry 原始条目（管理页只读 + 删除）。
type UserEntry struct {
	ID         string
	Role       string
	Content    string
	Type       string
	Scope      string
	Importance float64
	CreatedAt  time.Time
	ExpiresAt  *time.Time
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
			FactCount: e.FactCount, LastSeenAt: e.LastSeenAt, Scope: string(e.Scope),
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

func (s *MemoryService) ListUserFactsFiltered(ctx context.Context, req *ListUserFactsFilteredRequest) ([]*UserFactDetail, int, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = constants.DefaultPageSize
	}
	filter := domain.FactListFilter{
		Query: req.Query, ImportanceMin: req.ImportanceMin,
		ImportanceMax: req.ImportanceMax, Category: req.Category,
	}
	facts, err := s.factRepo.ListUserFactsFiltered(ctx, req.TenantID, req.UserID, filter, limit, req.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list user facts: %w", err)
	}
	total, err := s.factRepo.CountUserFactsFiltered(ctx, req.TenantID, req.UserID, filter)
	if err != nil {
		return nil, 0, fmt.Errorf("count user facts: %w", err)
	}
	out := make([]*UserFactDetail, 0, len(facts))
	for _, f := range facts {
		out = append(out, userFactDetailFromFact(f))
	}
	return out, total, nil
}

func (s *MemoryService) GetUserFact(ctx context.Context, tenantID, userID, factID string) (*UserFactDetail, error) {
	fact, err := s.factRepo.GetByID(ctx, tenantID, factID)
	if err != nil {
		return nil, err
	}
	if fact.UserID != userID {
		return nil, domain.ErrFactNotFound // 归属不匹配一律 404，不泄露存在性
	}
	return userFactDetailFromFact(fact), nil
}

// UpdateUserFact 编辑事实并同步向量。顺序（spec §3）：校验 + GetByID + 归属 →
// 新内容嵌入（失败 502 不写数据）→ 删旧向量（best-effort）→ PG update →
// upsert 新向量（失败返回 vectorSyncFailed=true，PG 已提交）。
func (s *MemoryService) UpdateUserFact(ctx context.Context, tenantID, userID, factID string, patch *UpdateUserFactPatch) (*UserFactDetail, bool, error) {
	if factPatchEmpty(patch) {
		return nil, false, domain.ErrEmptyFactPatch
	}
	fact, err := s.factRepo.GetByID(ctx, tenantID, factID)
	if err != nil {
		return nil, false, err
	}
	if fact.UserID != userID {
		return nil, false, domain.ErrFactNotFound
	}
	if fact.Status != domain.FactStatusActive {
		// 仅 active 可编辑：superseded/archived 归提取管线管辖，避免冲突（spec §5）。
		return nil, false, domain.ErrFactNotEditable
	}
	next, err := applyFactPatch(fact, patch)
	if err != nil {
		return nil, false, err
	}
	embedder, err := s.resolveEmbedClient(ctx, tenantID)
	if err != nil {
		return nil, false, err
	}
	vector, err := embedder.Embed(ctx, next.Content)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrMemoryEmbeddingUnavailable, err)
	}
	// 先删旧向量：陈旧内容立即停止被召回（best-effort，失败不阻塞主操作）。
	s.deleteFactVectorsBestEffort(ctx, tenantID, next.ID)
	if err := s.factRepo.Update(ctx, tenantID, next); err != nil {
		return nil, false, err
	}
	// 后写新向量：同 ID（fact.ID 是 Milvus 主键）不会与旧向量互相覆盖。
	if s.syncFactVector(ctx, tenantID, next, embedder, vector) {
		return userFactDetailFromFact(next), true, nil // 内容已保存，向量待后台补偿
	}
	return userFactDetailFromFact(next), false, nil
}

// factPatchEmpty 报告补丁是否为空（无任何可更新字段）。
func factPatchEmpty(patch *UpdateUserFactPatch) bool {
	return patch == nil || (patch.Content == nil && patch.Importance == nil && patch.Category == nil)
}

// applyFactPatch 在浅拷贝上应用补丁并校验；未触及字段原样保留。
func applyFactPatch(fact *domain.MemoryFact, patch *UpdateUserFactPatch) (*domain.MemoryFact, error) {
	next := *fact
	if patch.Content != nil {
		content := strings.TrimSpace(*patch.Content)
		if content == "" {
			return nil, domain.ErrEmptyContent
		}
		next.Content = content
	}
	if patch.Importance != nil {
		if *patch.Importance < 0 || *patch.Importance > 1 {
			return nil, domain.ErrImportanceOutOfRange
		}
		next.Importance = *patch.Importance
	}
	if patch.Category != nil {
		if !domain.IsValidFactCategory(*patch.Category) {
			return nil, domain.ErrInvalidCategory
		}
		next.Category = *patch.Category
	}
	next.UpdatedAt = time.Now().UTC()
	return &next, nil
}

// deleteFactVectorsBestEffort 删除事实旧向量；失败仅记录日志（GC reconcile 兜底）。
func (s *MemoryService) deleteFactVectorsBestEffort(ctx context.Context, tenantID, factID string) {
	if s.vectorStore == nil {
		return
	}
	if err := s.vectorStore.DeleteFactVectors(ctx, tenantID, []string{factID}); err != nil {
		s.logger.Error("memory: delete old fact vectors failed", zap.String("fact_id", factID), zap.Error(err))
	}
}

// syncFactVector 在 PG 提交后 upsert 新向量；失败仅记录日志并返回 true
// （内容已保存，向量待后台补偿，spec §3）。
func (s *MemoryService) syncFactVector(ctx context.Context, tenantID string, next *domain.MemoryFact, embedder port.EmbedClient, vector []float32) bool {
	if s.vectorStore == nil {
		return false
	}
	collection := factsCollectionName(tenantID, embedder.Model())
	doc := &port.VectorDoc{ID: next.ID, Embedding: vector, Metadata: factVectorMetadata(next)}
	if err := s.vectorStore.Upsert(ctx, collection, []*port.VectorDoc{doc}); err != nil {
		s.logger.Error("memory: upsert fact vector failed", zap.String("fact_id", next.ID), zap.Error(err))
		return true
	}
	return false
}

func (s *MemoryService) DeleteUserFact(ctx context.Context, tenantID, userID, factID string) error {
	fact, err := s.factRepo.GetByID(ctx, tenantID, factID)
	if err != nil {
		return err
	}
	if fact.UserID != userID {
		return domain.ErrFactNotFound
	}
	if err := s.factRepo.Delete(ctx, tenantID, factID); err != nil {
		return err
	}
	// PG 删除成功即操作成功；向量清理 best-effort，GC reconcile 兜底（spec §3）。
	if s.vectorStore != nil {
		if err := s.vectorStore.DeleteFactVectors(ctx, tenantID, []string{factID}); err != nil {
			s.logger.Error("memory: delete fact vectors failed", zap.String("fact_id", factID), zap.Error(err))
		}
	}
	return nil
}

func (s *MemoryService) DeleteUserEntity(ctx context.Context, tenantID, userID, entityID string) error {
	entity, err := s.entityRepo.GetByID(ctx, tenantID, entityID)
	if err != nil {
		return err
	}
	if entity.UserID != userID {
		return domain.ErrEntityNotFound
	}
	return s.entityRepo.Delete(ctx, tenantID, entityID)
}

// ListUserSummaries 返回用户历史摘要分页与总数（管理页只读；删除走 DeleteUserSummary）。
func (s *MemoryService) ListUserSummaries(ctx context.Context, tenantID, userID string, limit, offset int) ([]*UserSummary, int, error) {
	if s.historyRepo == nil {
		return nil, 0, fmt.Errorf("memory: history repo not wired")
	}
	segments, err := s.historyRepo.ListUserSummaries(ctx, tenantID, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list user summaries: %w", err)
	}
	total, err := s.historyRepo.CountUserSummaries(ctx, tenantID, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("count user summaries: %w", err)
	}
	out := make([]*UserSummary, 0, len(segments))
	for _, h := range segments {
		out = append(out, &UserSummary{
			ID: h.ID, Summary: h.Summary, Tier: h.Tier,
			ConversationID: h.ConversationID, Importance: h.Importance,
			PeriodEnd: h.PeriodEnd, CreatedAt: h.CreatedAt, Scope: string(h.Scope),
		})
	}
	return out, total, nil
}

// DeleteUserSummary 删除用户摘要；summary 不存在由 repo 返回 ErrSummaryNotFound。
func (s *MemoryService) DeleteUserSummary(ctx context.Context, tenantID, userID, summaryID string) error {
	if s.historyRepo == nil {
		return fmt.Errorf("memory: history repo not wired")
	}
	return s.historyRepo.Delete(ctx, tenantID, userID, summaryID)
}

// ListUserSnapshots 返回用户全部活跃快照（含过期/inactive，管理页需展示并允许清空）。
func (s *MemoryService) ListUserSnapshots(ctx context.Context, tenantID, userID string) ([]*UserSnapshot, error) {
	if s.activeSnapshotRepo == nil {
		return nil, fmt.Errorf("memory: active snapshot repo not wired")
	}
	snapshots, err := s.activeSnapshotRepo.ListUser(ctx, tenantID, userID)
	if err != nil {
		return nil, fmt.Errorf("list user snapshots: %w", err)
	}
	out := make([]*UserSnapshot, 0, len(snapshots))
	for _, sn := range snapshots {
		out = append(out, userSnapshotFromDomain(sn))
	}
	return out, nil
}

// UpdateUserSnapshot 编辑快照三段内容。保留既有 Source（注入/反思来源），
// 重置 expires_at 为 now+TTL 并以 UpdatedAt=now 绕过 Upsert 的覆盖守卫（用户显式
// 操作优先，spec §2 归属校验 + §5 边界）。快照不存在时 404。
func (s *MemoryService) UpdateUserSnapshot(ctx context.Context, tenantID, userID, agentID string, patch *UpdateUserSnapshotPatch) (*UserSnapshot, error) {
	if s.activeSnapshotRepo == nil {
		return nil, fmt.Errorf("memory: active snapshot repo not wired")
	}
	if patch == nil {
		return nil, domain.ErrSnapshotNotFound // 防御：无 body 视为不存在
	}
	snapshots, err := s.activeSnapshotRepo.ListUser(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	var existing *domain.ActiveSnapshot
	for _, sn := range snapshots {
		if sn.AgentID == agentID {
			existing = sn
			break
		}
	}
	if existing == nil {
		return nil, domain.ErrSnapshotNotFound
	}
	now := time.Now().UTC()
	next := domain.ActiveSnapshot{
		TenantID: tenantID, UserID: userID, AgentID: agentID,
		WorkContext: patch.WorkContext, PersonalContext: patch.PersonalContext, TopOfMind: patch.TopOfMind,
		Source: existing.Source, ExpiresAt: now.Add(constants.ActiveSnapshotTTL),
		UpdatedAt: now, Status: domain.SnapshotStatusActive,
	}
	if err := next.Validate(); err != nil {
		return nil, err
	}
	if err := s.activeSnapshotRepo.Upsert(ctx, &next); err != nil {
		return nil, err
	}
	return userSnapshotFromDomain(&next), nil
}

// DeleteUserSnapshot 清空指定 agent 的快照；agent 不存在时 404（不泄露存在性）。
func (s *MemoryService) DeleteUserSnapshot(ctx context.Context, tenantID, userID, agentID string) error {
	if s.activeSnapshotRepo == nil {
		return fmt.Errorf("memory: active snapshot repo not wired")
	}
	snapshots, err := s.activeSnapshotRepo.ListUser(ctx, tenantID, userID)
	if err != nil {
		return err
	}
	found := false
	for _, sn := range snapshots {
		if sn.AgentID == agentID {
			found = true
			break
		}
	}
	if !found {
		return domain.ErrSnapshotNotFound
	}
	return s.activeSnapshotRepo.Delete(ctx, tenantID, userID, agentID)
}

// ListUserEntries 返回用户原始条目分页与总数（query 为内容模糊匹配，管理页只读 + 删除）。
func (s *MemoryService) ListUserEntries(ctx context.Context, tenantID, userID string, limit, offset int, query string) ([]*UserEntry, int, error) {
	if s.memoryRepo == nil {
		return nil, 0, fmt.Errorf("memory: memory repo not wired")
	}
	entries, err := s.memoryRepo.ListUserEntries(ctx, tenantID, userID, limit, offset, query)
	if err != nil {
		return nil, 0, fmt.Errorf("list user entries: %w", err)
	}
	total, err := s.memoryRepo.CountUserEntries(ctx, tenantID, userID, query)
	if err != nil {
		return nil, 0, fmt.Errorf("count user entries: %w", err)
	}
	out := make([]*UserEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, &UserEntry{
			ID: e.ID, Role: e.Role, Content: e.Content, Type: e.Type, Scope: e.Scope,
			Importance: e.Importance, CreatedAt: e.CreatedAt, ExpiresAt: e.ExpiresAt,
		})
	}
	return out, total, nil
}

// DeleteUserEntry 删除归属当前用户的原始条目并 best-effort 清理向量：
// PG 删除成功即操作成功，向量清理失败仅记日志（GC reconcile 兜底）。
func (s *MemoryService) DeleteUserEntry(ctx context.Context, tenantID, userID, entryID string) error {
	if s.memoryRepo == nil {
		return fmt.Errorf("memory: memory repo not wired")
	}
	entry, err := s.memoryRepo.Get(ctx, tenantID, entryID)
	if err != nil {
		return err
	}
	if entry.UserID != userID {
		return domain.ErrEntryNotFound // 归属不匹配一律 404，不泄露存在性
	}
	if err := s.memoryRepo.Delete(ctx, tenantID, entryID); err != nil {
		return err
	}
	if s.vectorStore != nil {
		if err := s.vectorStore.DeleteEntryVectors(ctx, tenantID, []string{entryID}); err != nil {
			s.logger.Error("memory: delete entry vectors failed", zap.String("entry_id", entryID), zap.Error(err))
		}
	}
	return nil
}

// userSnapshotFromDomain 将领域快照映射为管理页 DTO（Status 转换为字符串）。
func userSnapshotFromDomain(sn *domain.ActiveSnapshot) *UserSnapshot {
	return &UserSnapshot{
		AgentID: sn.AgentID, AgentName: sn.AgentName, ConversationName: sn.ConversationName,
		WorkContext: sn.WorkContext, PersonalContext: sn.PersonalContext,
		TopOfMind: sn.TopOfMind, ExpiresAt: sn.ExpiresAt, UpdatedAt: sn.UpdatedAt, Status: string(sn.Status),
	}
}
