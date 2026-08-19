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
}

func buildAudit(db *pgxpool.Pool) *Audit {
	if db == nil {
		return nil
	}
	return &Audit{
		QueryService:         persistence.NewPgResourceChangeAuditRepo(db),
		PlatformQueryService: persistence.NewPgResourceChangeAuditRepo(db),
	}
}
