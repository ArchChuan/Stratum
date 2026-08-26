package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// ResourceKind 标识被版本化的产品资源类型。与 resource_versions 表的 CHECK 约束一致，
// 并预留 skill/knowledge/mcp 供后续阶段接入。
type ResourceKind string

const (
	ResourceKindAgent     ResourceKind = "agent"
	ResourceKindSkill     ResourceKind = "skill"
	ResourceKindKnowledge ResourceKind = "knowledge"
	ResourceKindMCP       ResourceKind = "mcp"
)

// VersionStatus 与 skill 版本机制一致：无 draft（保存即生效），仅 published/deprecated。
type VersionStatus string

const (
	VersionStatusPublished  VersionStatus = "published"
	VersionStatusDeprecated VersionStatus = "deprecated"
)

// VersionSource 区分人工保存与回滚产生的状态翻转。评测优化(source=optimization)
// 属于 resource_revisions 控制面，不写本表。
type VersionSource string

const (
	VersionSourceManual   VersionSource = "manual"
	VersionSourceRollback VersionSource = "rollback"
)

// Version 是产品资源可编辑面的版本化快照，供版本历史展示与回滚重建。
// 产品表通过 active_version_id 指向当前生效版本；回滚只翻转 status，不产生新版本。
type Version struct {
	ID              string
	ResourceKind    ResourceKind
	ResourceID      string
	ParentVersionID string
	RevisionNo      int
	Status          VersionStatus
	Source          VersionSource
	ContentHash     string
	Payload         map[string]any
	SafeSummary     map[string]any
	// CreatedBy 记录变更人 id（非资源所有权）。
	CreatedBy string
	// CreatedByName 是 CreatedBy 的展示名（display_name > github_login > actor_id），
	// display-only，在返回边界解析。
	CreatedByName string
	CreatedAt     time.Time
	PublishedAt   *time.Time
	// IsCurrent 标记产品表 active_version_id 指向的版本。仅 ListVersions 填充。
	IsCurrent bool
}

// ComputeContentHash 对 payload 做 canonical JSON 的 sha256（Go 的 encoding/json
// 对 map 键排序，输出确定），作为版本内容指纹与去重基线。
func (v Version) ComputeContentHash() (string, error) {
	encoded, err := json.Marshal(v.Payload)
	if err != nil {
		return "", fmt.Errorf("marshal version payload: %w", err)
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}
