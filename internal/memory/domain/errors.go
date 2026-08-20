// Package domain holds memory entities, value objects, and sentinels.
package domain

import "errors"

// ErrEntryNotFound is returned when a memory entry lookup misses.
var ErrEntryNotFound = errors.New("memory entry not found")

// ErrSessionNotFound is returned when a session has no entries / summary.
var ErrSessionNotFound = errors.New("memory session not found")

// Memory v2 error sentinels.
var (
	// ErrFactNotFound is returned when a memory fact lookup misses.
	ErrFactNotFound = errors.New("memory fact not found")

	// ErrEntityNotFound is returned when a memory entity lookup misses.
	ErrEntityNotFound = errors.New("memory entity not found")

	// ErrAgentMemoryDisabled is returned when memory operations are attempted on an agent with memory disabled.
	ErrAgentMemoryDisabled = errors.New("agent memory disabled")

	// ErrScopeMismatch is returned when read/write scope validation fails.
	ErrScopeMismatch = errors.New("memory scope mismatch")

	// ErrFactQuotaExceeded is returned when a user exceeds their fact quota.
	ErrFactQuotaExceeded = errors.New("memory fact quota exceeded")

	// ErrFactAlreadyDeleted is returned when attempting to operate on a soft-deleted fact.
	ErrFactAlreadyDeleted = errors.New("memory fact already deleted")

	// ErrInvalidStatus is returned when an invalid status transition is attempted.
	ErrInvalidStatus = errors.New("invalid memory fact status")

	// ErrUserIDMismatch is returned when userID validation fails.
	ErrUserIDMismatch = errors.New("memory fact userID required")

	// ErrEmptyContent is returned when fact content is empty.
	ErrEmptyContent = errors.New("memory fact content cannot be empty")

	// ErrInvalidCategory is returned when a fact category is not in the allowlist (Phase 0).
	ErrInvalidCategory = errors.New("memory fact category not in allowlist")

	// ErrConfidenceOutOfRange is returned when confidence is outside [0, 1] (Phase 0).
	ErrConfidenceOutOfRange = errors.New("memory fact confidence must be in [0, 1]")

	// ErrInvalidSource is returned when fact provenance is not recognized.
	ErrInvalidSource = errors.New("memory fact source not in allowlist")

	// ErrInvalidFactSourceIdentity is returned when replay-safe extraction provenance is incomplete.
	ErrInvalidFactSourceIdentity = errors.New("invalid memory fact source identity")

	// ErrFactSourceConflict is returned when one source identity is reused for a different payload.
	ErrFactSourceConflict = errors.New("memory fact source identity payload conflict")

	// ErrMigrationAlreadyActive 租户已有进行中迁移（唯一 active 不变量），拒绝重复触发。
	ErrMigrationAlreadyActive = errors.New("memory migration already active")
	// ErrMigrationNotFound 迁移记录不存在。
	ErrMigrationNotFound = errors.New("memory migration not found")
	// ErrMigrationInvalidTenant 迁移租户为空。
	ErrMigrationInvalidTenant = errors.New("memory migration tenant required")
	// ErrMigrationEmptyModel 迁移起止模型不能为空。
	ErrMigrationEmptyModel = errors.New("memory migration model required")
	// ErrMigrationSameModel 起止模型相同，无迁移必要。
	ErrMigrationSameModel = errors.New("memory migration from and to model must differ")
	// ErrMigrationNotActive 迁移不在 migrating 状态，无法推进/完成（已被取消或失败）。
	ErrMigrationNotActive = errors.New("memory migration not active")
	// ErrMigrationProgressRegressed 进度不得回退（断点续传单调不减）。
	ErrMigrationProgressRegressed = errors.New("memory migration progress regressed")
	// ErrMigrationNotRetryable 只有 failed/canceled 迁移可重试。
	ErrMigrationNotRetryable = errors.New("memory migration not retryable")
	// ErrMigrationUnknownModel 目标模型不是目录中可解析的嵌入模型，拒绝启动迁移
	// （fail-closed：避免生效模型被切到无效模型，产生不可回填的僵尸迁移）。
	ErrMigrationUnknownModel = errors.New("memory migration target model not resolvable")
)
