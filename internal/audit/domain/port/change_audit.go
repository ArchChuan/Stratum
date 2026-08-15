package port

import (
	"context"
	"encoding/json"
	"time"
)

// ResourceChangeAuditRow 是查询返回的一行只读模型。Before/After 为原样存储的
// JSON 投影（{} 表示 create 等无前值场景）。
type ResourceChangeAuditRow struct {
	ID           string
	ResourceKind string
	ResourceID   string
	Operation    string
	ActorID      string
	ActorName    string // display_name > github_login > actor_id 兜底（system actor 直接 actor_id）
	CreatedAt    time.Time
	Before       json.RawMessage
	After        json.RawMessage
}

// ResourceChangeAuditFilter 列表筛选。Limit<=0 或 Offset<0 由实现归一化为无分页。
type ResourceChangeAuditFilter struct {
	ResourceKind string
	ActorName    string // 子串模糊匹配 display_name/github_login/actor_id 原文
	From         *time.Time
	To           *time.Time
	Limit        int
	Offset       int
}

// ResourceChangeAuditQuery 列出 tenant-scoped 资源变更审计。每个方法都要求
// 非空 tenantID（fail closed）；SQL 中 tenant_id 谓词恒存在，禁止条件性追加。
type ResourceChangeAuditQuery interface {
	List(ctx context.Context, tenantID string, filter ResourceChangeAuditFilter) ([]ResourceChangeAuditRow, int, error)
	// GetByID 查不到时返回 (nil, nil)。
	GetByID(ctx context.Context, tenantID, id string) (*ResourceChangeAuditRow, error)
}
