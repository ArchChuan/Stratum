package application

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// EmbedClientResolverByModel resolves a per-tenant, per-model EmbedClient at
// call time. Migration backfill needs the model dimension (not just the tenant
// default): it re-embeds every existing fact with the explicit target model,
// independent of the tenant's current effective setting.
type EmbedClientResolverByModel func(ctx context.Context, tenantID, model string) port.EmbedClient

// TenantLister enumerates tenant IDs for the background backfill worker.
// Wiring injects the IAM active-tenant list (ListActiveTenantIDs).
type TenantLister func(ctx context.Context) ([]string, error)

// EffectiveModelSetter flips the tenant's effective memory embedding model.
// Wiring injects TenantService.SetSetting("memory_embedding_model", model).
// StartMigration runs it only after the migration record is durable.
type EffectiveModelSetter func(ctx context.Context, tenantID, model string) error

// EmbeddingModelValidator 校验目标模型是目录中可解析的嵌入模型（fail-closed）。
// Wiring 注入 LLMGateway Registry.ResolveEmbeddingExact；StartMigration 在登记
// 迁移前调用，返回错误则拒绝启动——避免生效模型被切到不存在的模型，产生无法
// 回填的僵尸迁移。
type EmbeddingModelValidator func(ctx context.Context, tenantID, model string) error

// MigrationCost 是确认制切换的成本预览：存量已提取事实条数 + 预计回填时长。
// 仅用于管理界面展示，非精确计量。
type MigrationCost struct {
	FactCount        int
	EstimatedSeconds int64
}

// MemoryMigrationService 编排「记忆嵌入模型平滑迁移」（P5 确认制切换）：
//
//   - StartMigration：登记 A→B 迁移记录并立即切换生效模型（新数据直接进 B），
//     存量事实由后台回填 worker 渐进 re-embed 到 B collection（幂等 Upsert）。
//   - 回填主数据源 = memory_facts 表已提取事实（key=fact.ID），断点续传按
//     progress 推进；迁移期读取由调用方复用 vectorSearchCandidates 双名兜底
//     （读 B + legacy A 回退）+ trigram 兜底，本服务不参与读取路径。
//   - 状态机 {migrating|done|failed|canceled}；failed/canceled 支持重试续传，
//     旧集合 memory_facts_{t}_A 退役保留（可回滚）。
//
// 后台回填按 BufferScanner worker 模式运行：Start/run(ticker)/Stop，panic 后
// 由 Start 外层重启。所有仓库访问逐任务 execTenant（租户隔离）。
type MemoryMigrationService struct {
	migrationRepo  port.MigrationRepo
	factRepo       port.FactRepo
	vectorStore    port.VectorStore
	embedResolver  EmbedClientResolverByModel
	listTenants    TenantLister
	setModel       EffectiveModelSetter
	modelValidator EmbeddingModelValidator
	logger         *zap.Logger
	// metrics 上报 memory_migration_progress gauge 与 stalled 计数器（P6）；
	// wiring 注入，未注入时 NoopMetrics 无副作用。
	metrics observability.MetricsProvider
	// stalledSince 记录每租户迁移上次扫描到的进度，用于跨扫描间隔的停滞判定。
	// 仅由 worker 单 goroutine（ProcessPending）读写，无需加锁。
	stalledSince map[string]int

	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewMemoryMigrationService 构造迁移服务。embedResolver / listTenants /
// setModel 由 wiring 在构图后注入（Set* 方法），与 MemoryService 的
// SetVectorStore / SetEmbedClientResolver 模式一致。
func NewMemoryMigrationService(
	migrationRepo port.MigrationRepo,
	factRepo port.FactRepo,
	vectorStore port.VectorStore,
	logger *zap.Logger,
) *MemoryMigrationService {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &MemoryMigrationService{
		migrationRepo: migrationRepo,
		factRepo:      factRepo,
		vectorStore:   vectorStore,
		logger:        logger,
		metrics:       observability.NoopMetrics{},
		stalledSince:  make(map[string]int),
		stopCh:        make(chan struct{}),
	}
}

// SetMetrics 注入指标上报（P6：进度 gauge / 停滞计数器）。
func (s *MemoryMigrationService) SetMetrics(m observability.MetricsProvider) { s.metrics = m }

// reportProgress 把迁移当前进度与状态写入 gauge（任意状态变化/推进后调用）。
func (s *MemoryMigrationService) reportProgress(m *domain.MemoryMigration) {
	s.metrics.SetMemoryMigrationProgress(m.TenantID, m.FromModel, m.ToModel, string(m.Status), m.Progress)
}

func stalledKey(tenantID string, id int64) string {
	return tenantID + ":" + strconv.FormatInt(id, 10)
}

// SetEmbedResolver wires the per-model embed resolver used by backfill.
func (s *MemoryMigrationService) SetEmbedResolver(r EmbedClientResolverByModel) { s.embedResolver = r }

// SetTenantLister wires the active-tenant enumerator used by the worker.
func (s *MemoryMigrationService) SetTenantLister(l TenantLister) { s.listTenants = l }

// SetEffectiveModelSetter wires the effective-model switch callback used by
// StartMigration (确认制：登记迁移后立即切生效模型，新数据直接进 B）。
func (s *MemoryMigrationService) SetEffectiveModelSetter(f EffectiveModelSetter) { s.setModel = f }

// SetModelValidator wires the target-model resolvability guard. StartMigration
// rejects structurally-valid-but-unresolvable target models before they can be
// persisted or switched into the tenant's effective setting.
func (s *MemoryMigrationService) SetModelValidator(v EmbeddingModelValidator) { s.modelValidator = v }

// validateTargetModel 校验目标模型在目录中可解析（fail-closed）。validator 未
// 注入时直接放行（测试构造场景）；失败统一 wrap 领域 sentinel 以映射 400，
// 绝不 5xx。
func (s *MemoryMigrationService) validateTargetModel(ctx context.Context, tenantID, toModel string) error {
	if s.modelValidator == nil {
		return nil
	}
	if err := s.modelValidator(ctx, tenantID, toModel); err != nil {
		return fmt.Errorf("start migration: %w (%w)", domain.ErrMigrationUnknownModel, err)
	}
	return nil
}

// StartMigration 确认制启动一次 A→B 迁移：
//
//  1. 校验租户无进行中迁移（唯一 active 不变量）。
//  2. 快照 memory_facts 行数作为 progress.total（分母不随迁移期间写入漂移）。
//  3. 先持久化迁移记录（migrating, 零进度），再切换生效模型；切换失败回滚
//     迁移到 canceled 并返回错误，避免「迁移已登记但生效模型未切」的悬挂态。
//
// 返回登记后的迁移（含 DB 生成的 id），回填由后台 worker 异步推进。
func (s *MemoryMigrationService) StartMigration(ctx context.Context, tenantID, fromModel, toModel string) (*domain.MemoryMigration, error) {
	if tenantID == "" {
		return nil, domain.ErrMigrationInvalidTenant
	}
	if s.setModel == nil {
		// wiring 未注入生效模型切换回调：fail-closed，避免登记了迁移却切不了模型。
		return nil, fmt.Errorf("start migration: effective model setter is not wired")
	}
	active, err := s.migrationRepo.GetActive(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("start migration: check active: %w", err)
	}
	if active != nil {
		return nil, fmt.Errorf("start migration: %w (id=%d from=%s to=%s)",
			domain.ErrMigrationAlreadyActive, active.ID, active.FromModel, active.ToModel)
	}
	total, err := s.factRepo.CountAll(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("start migration: count facts: %w", err)
	}
	m, err := domain.NewMigration(tenantID, fromModel, toModel, total)
	if err != nil {
		return nil, fmt.Errorf("start migration: %w", err)
	}
	// fail-closed：目标模型必须是目录中可解析的嵌入模型，否则拒绝启动，防止生效
	// 模型被切到无效模型（产生不可回填的僵尸迁移）。校验放在结构校验之后，保证
	// 空/相同模型的错误语义由 NewMigration 优先给出。validator 未注入时跳过
	// （测试构造场景）；生产 wiring 与 setModel 成对注入，均以 Registry 可用为前提。
	if err := s.validateTargetModel(ctx, tenantID, toModel); err != nil {
		return nil, err
	}
	id, err := s.migrationRepo.Create(ctx, tenantID, m)
	if err != nil {
		return nil, fmt.Errorf("start migration: create record: %w", err)
	}
	m.ID = id

	if err := s.setModel(ctx, tenantID, toModel); err != nil {
		_, _ = s.migrationRepo.Complete(ctx, tenantID, id, domain.MigrationStatusCanceled)
		return nil, fmt.Errorf("start migration: switch effective model: %w", err)
	}
	s.reportProgress(m)

	s.logger.Info("memory.migration.started",
		zap.String("tenant_id", tenantID), zap.Int64("id", id),
		zap.String("from", fromModel), zap.String("to", toModel), zap.Int("total", total))
	return m, nil
}

// CancelMigration 取消进行中的迁移（保留进度，可 Retry 续传）。确认制下生效
// 模型已切换且不随取消回退（回滚是另一条显式的反向迁移）；done 与 failed 状态
// 不接受取消（failed 走 Retry）。
func (s *MemoryMigrationService) CancelMigration(ctx context.Context, tenantID string, id int64) error {
	m, err := s.migrationRepo.GetByID(ctx, tenantID, id)
	if err != nil {
		return fmt.Errorf("cancel migration: %w", err)
	}
	switch m.Status {
	case domain.MigrationStatusDone, domain.MigrationStatusFailed:
		return domain.ErrMigrationNotActive
	case domain.MigrationStatusCanceled:
		return nil // 幂等：已取消
	}
	if err := m.Cancel(); err != nil {
		return fmt.Errorf("cancel migration: %w", err)
	}
	ok, err := s.migrationRepo.Complete(ctx, tenantID, id, domain.MigrationStatusCanceled)
	if err != nil {
		return fmt.Errorf("cancel migration: %w", err)
	}
	if !ok {
		return domain.ErrMigrationNotActive
	}
	m.Status = domain.MigrationStatusCanceled
	s.reportProgress(m)
	s.logger.Info("memory.migration.canceled", zap.String("tenant_id", tenantID), zap.Int64("id", id))
	return nil
}

// RetryMigration 把 failed/canceled 迁移重置为 migrating，从既有 progress 断点
// 续传（Restart 原子只命中可重试状态）。生效模型早已切换，无需再次 setModel。
func (s *MemoryMigrationService) RetryMigration(ctx context.Context, tenantID string, id int64) error {
	m, err := s.migrationRepo.GetByID(ctx, tenantID, id)
	if err != nil {
		return fmt.Errorf("retry migration: %w", err)
	}
	if err := m.Retry(); err != nil {
		return fmt.Errorf("retry migration: %w", err)
	}
	ok, err := s.migrationRepo.Restart(ctx, tenantID, id)
	if err != nil {
		return fmt.Errorf("retry migration: %w", err)
	}
	if !ok {
		return domain.ErrMigrationNotRetryable
	}
	s.reportProgress(m)
	s.logger.Info("memory.migration.retried", zap.String("tenant_id", tenantID), zap.Int64("id", id),
		zap.Int("progress", m.Progress))
	return nil
}

// GetCurrent 返回租户最近一条迁移（任意状态）；无则返回 (nil, nil)。供管理界面
// 展示当前迁移状态/进度，也供 handler 判断是否存在 active 迁移。
func (s *MemoryMigrationService) GetCurrent(ctx context.Context, tenantID string) (*domain.MemoryMigration, error) {
	return s.migrationRepo.GetLatest(ctx, tenantID)
}

// CostPreview 计算迁移成本预览（存量已提取事实条数 + 预计回填时长）。
func (s *MemoryMigrationService) CostPreview(ctx context.Context, tenantID string) (*MigrationCost, error) {
	total, err := s.factRepo.CountAll(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("cost preview: count facts: %w", err)
	}
	estMS := int64(total) * constants.MemoryMigrationPerFactEstimateMS
	return &MigrationCost{
		FactCount:        total,
		EstimatedSeconds: (estMS + 999) / 1000, // ceil 到秒
	}, nil
}

// ProcessPending 扫描所有租户的 active 迁移并逐任务回填（worker 每 tick 调用；
// 测试可直接调用）。任一租户的回填失败只标记该迁移 failed 并继续后续租户，
// 不阻塞整体扫描。
func (s *MemoryMigrationService) ProcessPending(ctx context.Context) {
	if s.listTenants == nil {
		s.logger.Warn("memory.migration.skip_process: no tenant lister")
		return
	}
	tenantIDs, err := s.listTenants(ctx)
	if err != nil {
		s.logger.Error("memory.migration.list_tenants_failed", zap.Error(err))
		return
	}
	for _, tid := range tenantIDs {
		if err := ctx.Err(); err != nil {
			return
		}
		active, err := s.migrationRepo.GetActive(ctx, tid)
		if err != nil {
			s.logger.Error("memory.migration.get_active_failed", zap.String("tenant_id", tid), zap.Error(err))
			continue
		}
		if active == nil {
			continue
		}
		s.backfillMigration(ctx, active)
		s.trackStall(active)
	}
}

// trackStall 跨扫描间隔对比迁移进度：连续两拍未推进且仍 migrating 视为停滞，
// 上报 stalled 计数器（P6 告警数据源）。仅 worker goroutine 调用。
func (s *MemoryMigrationService) trackStall(m *domain.MemoryMigration) {
	key := stalledKey(m.TenantID, m.ID)
	if !m.Status.Terminal() {
		last, seen := s.stalledSince[key]
		if seen && last == m.Progress {
			s.metrics.IncMemoryMigrationStalled(m.TenantID, m.FromModel, m.ToModel)
		}
		s.stalledSince[key] = m.Progress
		return
	}
	delete(s.stalledSince, key)
}

// backfillMigration 把一个 active 迁移回填到完成或失败。以 fact.ID 幂等
// Upsert 到 memory_facts_{t}_{toModel}，progress 断点续传，mark done/failed
// 均由仓储原子守卫 migrating 状态（并发取消安全）。
func (s *MemoryMigrationService) backfillMigration(ctx context.Context, m *domain.MemoryMigration) {
	pageSize := constants.MemoryMigrationPageSize
	for offset := m.Progress; offset < m.TotalFacts; {
		if !s.waitForScan(ctx) {
			return
		}
		next, done, err := s.backfillPage(ctx, m, offset, pageSize)
		if err != nil {
			s.failMigration(ctx, m, err)
			return
		}
		if done {
			s.completeMigration(ctx, m)
			return
		}
		if !s.commitProgress(ctx, m, next) {
			// 迁移已被并发取消/失败：仓储未命中 migrating 守卫，停止回填。
			s.logger.Warn("memory.migration.aborted",
				zap.String("tenant_id", m.TenantID), zap.Int64("id", m.ID))
			return
		}
		offset = next
		m.Progress = next
		s.reportProgress(m)
	}
	s.completeMigration(ctx, m)
}

// waitForScan 返回 false 表示 ctx 已取消或 worker 停止，应终止回填。
func (s *MemoryMigrationService) waitForScan(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-s.stopCh:
		return false
	default:
		return true
	}
}

// backfillPage 回填一页事实到目标向量集合，返回下一游标、是否完成与错误。
func (s *MemoryMigrationService) backfillPage(ctx context.Context, m *domain.MemoryMigration, offset, pageSize int) (int, bool, error) {
	facts, err := s.factRepo.ListAllFacts(ctx, m.TenantID, pageSize, offset)
	if err != nil {
		return 0, false, fmt.Errorf("list facts at offset %d: %w", offset, err)
	}
	if len(facts) == 0 {
		// 快照漂移兜底：total 是迁移开始时快照，期间 facts 可能被清理/删除，
		// 读到空页即无可回填，直接完成（幂等 Upsert 已保证已覆盖行）。
		return 0, true, nil
	}
	docs, err := s.buildDocs(ctx, m.TenantID, m.ToModel, facts)
	if err != nil {
		return 0, false, err
	}
	if err := s.vectorStore.Upsert(ctx, factsCollectionName(m.TenantID, m.ToModel), docs); err != nil {
		return 0, false, fmt.Errorf("upsert vectors: %w", err)
	}
	return offset + len(facts), false, nil
}

// commitProgress 原子推进游标；返回 false 表示 Advance 失败或迁移已被并发取消。
func (s *MemoryMigrationService) commitProgress(ctx context.Context, m *domain.MemoryMigration, next int) bool {
	ok, err := s.migrationRepo.Advance(ctx, m.TenantID, m.ID, next)
	if err != nil {
		s.failMigration(ctx, m, fmt.Errorf("advance progress to %d: %w", next, err))
		return false
	}
	return ok
}

// buildDocs 把一页事实 re-embed 成 B 模型的 VectorDoc（metadata 与实时提取
// extraction.go 完全一致，MilvusPortAdapter.memoryFactDocumentChunk 全部必需）。
func (s *MemoryMigrationService) buildDocs(ctx context.Context, tenantID, model string, facts []*domain.MemoryFact) ([]*port.VectorDoc, error) {
	if s.embedResolver == nil {
		return nil, fmt.Errorf("memory migration: no embed resolver")
	}
	embedder := s.embedResolver(ctx, tenantID, model)
	if embedder == nil {
		return nil, fmt.Errorf("memory migration: embed client unavailable for tenant %s model %s", tenantID, model)
	}
	docs := make([]*port.VectorDoc, 0, len(facts))
	for _, fact := range facts {
		// 回填只面向 active 事实：superseded/archived 的向量已被生命周期清理
		// 删除，迁移不得把非 active 事实重新嵌入复活。
		if fact.Status != domain.FactStatusActive {
			s.logger.Debug("memory.migration.skip_non_active",
				zap.String("tenant_id", tenantID),
				zap.String("fact_id", fact.ID),
				zap.String("status", string(fact.Status)))
			continue
		}
		vector, err := embedder.Embed(ctx, fact.Content)
		if err != nil {
			return nil, fmt.Errorf("memory migration: embed fact %s: %w", fact.ID, err)
		}
		docs = append(docs, &port.VectorDoc{
			ID:        fact.ID,
			Embedding: vector,
			Metadata: map[string]interface{}{
				"user_id":         fact.UserID,
				"agent_id":        fact.AgentID,
				"conversation_id": fact.ConversationID,
				"scope":           string(fact.Scope),
				"content":         fact.Content,
				"importance":      fact.Importance,
				"category":        fact.Category,
				"confidence":      fact.Confidence,
				"source":          fact.Source,
			},
		})
	}
	return docs, nil
}

func (s *MemoryMigrationService) completeMigration(ctx context.Context, m *domain.MemoryMigration) {
	ok, err := s.migrationRepo.Complete(ctx, m.TenantID, m.ID, domain.MigrationStatusDone)
	if err != nil {
		s.logger.Error("memory.migration.complete_failed",
			zap.String("tenant_id", m.TenantID), zap.Int64("id", m.ID), zap.Error(err))
		return
	}
	if !ok {
		s.logger.Warn("memory.migration.complete_miss",
			zap.String("tenant_id", m.TenantID), zap.Int64("id", m.ID))
		return
	}
	m.Status = domain.MigrationStatusDone
	s.reportProgress(m)
	s.logger.Info("memory.migration.done",
		zap.String("tenant_id", m.TenantID), zap.Int64("id", m.ID),
		zap.Int("total", m.TotalFacts), zap.Int("progress", m.Progress))
}

func (s *MemoryMigrationService) failMigration(ctx context.Context, m *domain.MemoryMigration, cause error) {
	ok, err := s.migrationRepo.Complete(ctx, m.TenantID, m.ID, domain.MigrationStatusFailed)
	if err != nil {
		s.logger.Error("memory.migration.fail_mark_failed",
			zap.String("tenant_id", m.TenantID), zap.Int64("id", m.ID), zap.Error(err))
		return
	}
	if !ok {
		s.logger.Warn("memory.migration.fail_miss",
			zap.String("tenant_id", m.TenantID), zap.Int64("id", m.ID))
		return
	}
	m.Status = domain.MigrationStatusFailed
	s.reportProgress(m)
	s.logger.Error("memory.migration.failed",
		zap.String("tenant_id", m.TenantID), zap.Int64("id", m.ID),
		zap.Int("progress", m.Progress), zap.Error(cause))
}

// Start 启动后台回填 worker：立即处理一次待办迁移（重启续传/新迁移秒级拾起），
// 之后按 MemoryMigrationScanInterval 轮询。run 返回（panic 恢复后）由外层
// select 决定重启或退出。
func (s *MemoryMigrationService) Start(ctx context.Context) {
	s.logger.Info("memory.migration.worker.start")
	for {
		s.run(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		default:
			s.logger.Warn("memory.migration.worker.restarting_after_panic")
			time.Sleep(time.Second)
		}
	}
}

func (s *MemoryMigrationService) run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("memory.migration.worker.panic", zap.Any("panic", r), zap.Stack("stack"))
		}
	}()
	// 首拍立即处理：Start 后无需等一个完整 interval。
	s.ProcessPending(ctx)
	ticker := time.NewTicker(constants.MemoryMigrationScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.ProcessPending(ctx)
		}
	}
}

// Stop 通知 worker 优雅退出（等当前回填页完成）。
func (s *MemoryMigrationService) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}
