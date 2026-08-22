package wiring

import (
	"github.com/jackc/pgx/v5/pgxpool"

	auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
	"github.com/byteBuilderX/stratum/internal/audit/infrastructure/persistence"
)

// Audit 承载资源变更审计的查询服务（平台级 HTTP 审计已废弃，见 spec F）。
type Audit struct {
	QueryService         auditport.ResourceChangeAuditQuery
	PlatformQueryService auditport.PlatformResourceChangeAuditQuery
	FailureRecorder      auditport.FailureAuditRecorder
}

func buildAudit(db *pgxpool.Pool) *Audit {
	if db == nil {
		return nil
	}
	return &Audit{
		QueryService:         persistence.NewPgResourceChangeAuditRepo(db),
		PlatformQueryService: persistence.NewPgResourceChangeAuditRepo(db),
		FailureRecorder:      persistence.NewPgFailureAuditRepo(db),
	}
}

// failureRecorderOf 返回全局失败资源操作审计记录器；审计未装配时返回 nil
// （各消费方在 nil 时跳过记录，不影响主流程）。
func failureRecorderOf(c *Container) auditport.FailureAuditRecorder {
	if c.Audit == nil {
		return nil
	}
	return c.Audit.FailureRecorder
}

// auditQueryOf 返回租户资源变更审计查询；审计未装配时返回 nil。
func auditQueryOf(c *Container) auditport.ResourceChangeAuditQuery {
	if c.Audit == nil {
		return nil
	}
	return c.Audit.QueryService
}
