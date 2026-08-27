package domain

import "time"

// MemoryEntryListItem 是管理页原始条目列表项（区别于写入路径的 MemoryEntry：
// memory_entries 表含 scope/created_at 等只读列，而 v1 MemoryEntry 未携带）。
type MemoryEntryListItem struct {
	ID         string
	Role       string
	Content    string
	Type       string
	Scope      string
	Importance float64
	CreatedAt  time.Time
	ExpiresAt  *time.Time // 可空：条目可无过期时间
}
