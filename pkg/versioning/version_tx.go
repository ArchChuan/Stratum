// Package versioning 提供通用产品版本历史基座的 tx-scoped 写 helper，供各资源
// context 的 repository 在自己租户写事务内调用（同 pkg/resourceaccess 范式），
// 实现「产品更新 + 版本写入」同事务原子性。通用核心不拥有写事务，只提供 helper。
//
// 本包是 pkg 共享层，自包含类型（不 import internal/）；调用方把 domain 实体
// 映射为 VersionRow 再传入。
package versioning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
)

// TableRef 描述产品表与其生效版本指针列，用于 SetActiveTx 与 is_current 推导。
type TableRef struct {
	Table        string
	ActiveColumn string
}

// productTables 仅注册已接入版本机制的产品表。未注册的 kind 必须 fail-closed：
// SetActiveTx / 读侧 is_current 一律报错，禁止静默跳过版本写入。
// 后续阶段接入 knowledge/mcp/skill 时在此登记对应表与指针列。
var productTables = map[string]TableRef{
	"agent":     {Table: "agents", ActiveColumn: "active_version_id"},
	"knowledge": {Table: "rag_workspaces", ActiveColumn: "active_version_id"},
}

// ProductTableRef 返回 kind 对应的产品表引用；未注册返回 false。
func ProductTableRef(kind string) (TableRef, bool) {
	ref, ok := productTables[kind]
	return ref, ok
}

// VersionRow 是写 helper 的最小输入，字段与 resource_versions 表逐列对应。
// 调用方负责构造 ID/Status/Source/Payload/SafeSummary/CreatedBy；
// revision_no、parent_version_id、content_hash 由 InsertVersionTx 计算回填。
type VersionRow struct {
	ID           string
	ResourceKind string
	ResourceID   string
	Status       string // published | deprecated
	Source       string // manual | rollback
	Payload      map[string]any
	SafeSummary  map[string]any
	CreatedBy    string
}

// savedVersion 是 InsertVersionTx 返回的完整行（回填计算字段）。
type savedVersion struct {
	VersionRow
	ParentVersionID string
	RevisionNo      int
	ContentHash     string
	PublishedAt     *time.Time
}

// InsertVersionTx 在调用方事务内插入一个新版本：revision_no 按 (kind, resource_id)
// 取 MAX+1（唯一部分索引防并发重复，冲突→唯一违反由调用方映射 409），parent 自链
// 到上一个最高版本号行，content_hash 由 payload canonical JSON 计算。返回回填后的行。
func InsertVersionTx(ctx context.Context, tx pgx.Tx, row VersionRow) (savedVersion, error) {
	var next int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(revision_no), 0) + 1 FROM resource_versions
		 WHERE resource_kind=$1 AND resource_id=$2`,
		row.ResourceKind, row.ResourceID,
	).Scan(&next); err != nil {
		return savedVersion{}, fmt.Errorf("versioning: next revision no: %w", err)
	}

	// parent 自链到上一行（新→旧顺序中最高版本号，排除自身）。
	var parentID string
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE((SELECT id FROM resource_versions
		  WHERE resource_kind=$1 AND resource_id=$2 AND id<>$3
		  ORDER BY revision_no DESC NULLS LAST, created_at DESC LIMIT 1), '')`,
		row.ResourceKind, row.ResourceID, row.ID,
	).Scan(&parentID); err != nil {
		return savedVersion{}, fmt.Errorf("versioning: resolve parent: %w", err)
	}

	hash, err := computeContentHash(row.Payload)
	if err != nil {
		return savedVersion{}, err
	}
	payloadJSON, err := json.Marshal(row.Payload)
	if err != nil {
		return savedVersion{}, fmt.Errorf("versioning: marshal payload: %w", err)
	}
	// nil 安全摘要落库为 {}（对齐 DB 默认值），避免 json.Marshal(nil) 产出 null。
	summaryJSON := []byte("{}")
	if row.SafeSummary != nil {
		summaryJSON, err = json.Marshal(row.SafeSummary)
		if err != nil {
			return savedVersion{}, fmt.Errorf("versioning: marshal safe summary: %w", err)
		}
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO resource_versions
		 (id, resource_kind, resource_id, parent_version_id, revision_no, status, source,
		  content_hash, payload, safe_summary, created_by, published_at)
		 VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, $9, $10, $11,
		         CASE WHEN $6 = 'published' THEN NOW() ELSE NULL END)`,
		row.ID, row.ResourceKind, row.ResourceID, parentID, next, row.Status, row.Source,
		hash, string(payloadJSON), string(summaryJSON), row.CreatedBy,
	); err != nil {
		return savedVersion{}, fmt.Errorf("versioning: insert version: %w", err)
	}

	return savedVersion{
		VersionRow:      row,
		ParentVersionID: parentID,
		RevisionNo:      next,
		ContentHash:     hash,
	}, nil
}

// DemoteCurrentTx 把当前生效版本降级为历史（可回滚）。首次保存（无 published）时
// 影响 0 行，不视为错误。
func DemoteCurrentTx(ctx context.Context, tx pgx.Tx, kind, resourceID string) error {
	if _, err := tx.Exec(ctx,
		`UPDATE resource_versions SET status='deprecated', updated_at=NOW()
		 WHERE resource_kind=$1 AND resource_id=$2 AND status='published'`,
		kind, resourceID,
	); err != nil {
		return fmt.Errorf("versioning: demote current: %w", err)
	}
	return nil
}

// RollbackVersionTx 回滚到目标历史版本：当前生效版本降级 + 目标 deprecated 恢复为
// published（RowsAffected==1 校验，非法目标返回错误）。不产生新版本。
func RollbackVersionTx(ctx context.Context, tx pgx.Tx, kind, resourceID, targetVersionID string) error {
	if err := DemoteCurrentTx(ctx, tx, kind, resourceID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx,
		`UPDATE resource_versions SET status='published', published_at=NOW(), updated_at=NOW()
		 WHERE id=$3 AND resource_kind=$1 AND resource_id=$2 AND status='deprecated'`,
		kind, resourceID, targetVersionID,
	)
	if err != nil {
		return fmt.Errorf("versioning: promote target: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrVersionNotFound
	}
	return nil
}

// SetActiveTx 更新产品表的 active_version_id 指针。kind 未注册产品表时 fail-closed。
func SetActiveTx(ctx context.Context, tx pgx.Tx, kind, resourceID, versionID string) error {
	ref, ok := ProductTableRef(kind)
	if !ok {
		return ErrVersionKindUnsupported
	}
	if !isSafeIdent(ref.Table) || !isSafeIdent(ref.ActiveColumn) {
		return fmt.Errorf("versioning: unsafe product table identifier %q.%q", ref.Table, ref.ActiveColumn)
	}
	if _, err := tx.Exec(ctx,
		fmt.Sprintf(`UPDATE %s SET %s=$2, updated_at=NOW() WHERE id=$1`, ref.Table, ref.ActiveColumn),
		resourceID, versionID,
	); err != nil {
		return fmt.Errorf("versioning: set active version: %w", err)
	}
	return nil
}

var safeIdent = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// isSafeIdent 校验 SQL 标识符仅含小写字母/数字/下划线，防止产品表映射污染 SQL。
func isSafeIdent(s string) bool { return safeIdent.MatchString(s) }

// computeContentHash 对 payload 做 canonical JSON 的 sha256。与 internal/versioning
// domain 的 Version.ComputeContentHash 同算法（domain 层校验用），pkg 层为写边界
// 自包含实现。
func computeContentHash(payload map[string]any) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("versioning: marshal payload for hash: %w", err)
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

var (
	// ErrVersionNotFound 由 RollbackVersionTx 在目标非 deprecated 时返回，调用方
	// 映射为 404。与 resourceaccess 的 notEligibleErr 参数同理：领域错误由调用方
	// 定义，避免 pkg 反向依赖 internal。
	ErrVersionNotFound = fmt.Errorf("versioning: target version not found or not deprecated")
	// ErrVersionKindUnsupported 由 SetActiveTx 在 kind 未注册产品表时返回。
	ErrVersionKindUnsupported = fmt.Errorf("versioning: resource kind has no product table")
)
