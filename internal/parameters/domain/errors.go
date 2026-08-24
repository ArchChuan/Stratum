package domain

import "errors"

// 平台配置版本化的业务错误。基础设施层翻译为这些哨兵错误，application 层
// 据此编排，handler 映射为 4xx/5xx。
var (
	// ErrGroupNotFound: 目标分组不存在（043 seed 外的非法 group_key）。
	ErrGroupNotFound = errors.New("platform config group not found")
	// ErrVersionNotFound: 目标版本 ID 不存在。
	ErrVersionNotFound = errors.New("platform config version not found")
	// ErrVersionNotDraft: 只有 draft 状态可发布（published/archived 只读）。
	ErrVersionNotDraft = errors.New("only draft versions can be published")
	// ErrVersionNotPublished: 只有 published 状态可回滚到。
	ErrVersionNotPublished = errors.New("only published versions can be rolled back to")
	// ErrConcurrentPublish: version_seq 分配或 label 挪动的并发冲突。
	ErrConcurrentPublish = errors.New("concurrent platform config modification")
)
