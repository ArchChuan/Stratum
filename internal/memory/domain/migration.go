package domain

import "time"

// MigrationStatus 是记忆嵌入模型迁移的状态机状态。
// migrating → done|failed|canceled；failed/canceled → migrating（重试）。
type MigrationStatus string

const (
	// MigrationStatusMigrating 迁移进行中（回填 worker 持续推进）。
	MigrationStatusMigrating MigrationStatus = "migrating"
	// MigrationStatusDone 回填完成，迁移期读取切只读 B、旧集合退役保留。
	MigrationStatusDone MigrationStatus = "done"
	// MigrationStatusFailed 回填中断（embed/存储失败或进程重启），支持重试。
	MigrationStatusFailed MigrationStatus = "failed"
	// MigrationStatusCanceled 管理员取消，支持重试。
	MigrationStatusCanceled MigrationStatus = "canceled"
)

// Valid 报告状态值是否属于合法枚举。
func (s MigrationStatus) Valid() bool {
	switch s {
	case MigrationStatusMigrating, MigrationStatusDone, MigrationStatusFailed, MigrationStatusCanceled:
		return true
	}
	return false
}

// Terminal 报告状态是否终态（done/failed/canceled 不再被回填 worker 处理）。
func (s MigrationStatus) Terminal() bool {
	return s == MigrationStatusDone || s == MigrationStatusFailed || s == MigrationStatusCanceled
}

// MemoryMigration 是一次「from 模型 → to 模型」的记忆向量迁移任务。
// Progress 表示已回填到 memory_facts 的第几行（断点续传游标，按 id 排序），
// TotalFacts 是迁移开始时 memory_facts 行数快照（进度分母，不随写入漂移）。
type MemoryMigration struct {
	ID         int64
	TenantID   string
	FromModel  string
	ToModel    string
	Status     MigrationStatus
	Progress   int
	TotalFacts int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// NewMigration 构造一个初始 migrating 状态、零进度的迁移。
func NewMigration(tenantID, fromModel, toModel string, totalFacts int) (*MemoryMigration, error) {
	if tenantID == "" {
		return nil, ErrMigrationInvalidTenant
	}
	if fromModel == "" || toModel == "" {
		return nil, ErrMigrationEmptyModel
	}
	if fromModel == toModel {
		return nil, ErrMigrationSameModel
	}
	if totalFacts < 0 {
		totalFacts = 0
	}
	now := now()
	return &MemoryMigration{
		TenantID:   tenantID,
		FromModel:  fromModel,
		ToModel:    toModel,
		Status:     MigrationStatusMigrating,
		Progress:   0,
		TotalFacts: totalFacts,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

// Advance 把进度推进到 next（单调不减），仅在 migrating 状态允许。
// 返回 errMigrationNotActive 当迁移已不在 migrating（被取消/失败），调用方应停止回填。
func (m *MemoryMigration) Advance(next int) error {
	if m.Status != MigrationStatusMigrating {
		return ErrMigrationNotActive
	}
	if next < m.Progress {
		return ErrMigrationProgressRegressed
	}
	m.Progress = next
	m.UpdatedAt = now()
	return nil
}

// MarkDone 完成回填：进度对齐快照总数，状态置 done。
func (m *MemoryMigration) MarkDone() error {
	if m.Status != MigrationStatusMigrating {
		return ErrMigrationNotActive
	}
	m.Status = MigrationStatusDone
	m.Progress = m.TotalFacts
	m.UpdatedAt = now()
	return nil
}

// MarkFailed 标记回填失败，保留当前进度以便重试续传。
func (m *MemoryMigration) MarkFailed() error {
	if m.Status != MigrationStatusMigrating {
		return ErrMigrationNotActive
	}
	m.Status = MigrationStatusFailed
	m.UpdatedAt = now()
	return nil
}

// Cancel 取消迁移，保留当前进度以便重试续传。
func (m *MemoryMigration) Cancel() error {
	if m.Status != MigrationStatusMigrating {
		return ErrMigrationNotActive
	}
	m.Status = MigrationStatusCanceled
	m.UpdatedAt = now()
	return nil
}

// Retry 把 failed/canceled 迁移重置为 migrating，从已有 progress 断点续传。
func (m *MemoryMigration) Retry() error {
	if m.Status != MigrationStatusFailed && m.Status != MigrationStatusCanceled {
		return ErrMigrationNotRetryable
	}
	m.Status = MigrationStatusMigrating
	m.UpdatedAt = now()
	return nil
}
