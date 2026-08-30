package port

import (
	"context"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
)

// ResourceEditorRepo manages the shared resource_editors whitelist for a
// workflow resource (kind='workflow'). Whitelist members may edit the
// workflow (Update/Publish); the申请通道 approval grants membership here.
type ResourceEditorRepo interface {
	// ListEditors returns the granted editor ids, oldest grant first.
	ListEditors(context.Context, string, string) ([]string, error)
	// ReplaceEditors atomically swaps the editor set and records the change
	// audit inside the same transaction. createdBy is the acting admin/owner.
	ReplaceEditors(context.Context, string, string, []string, string, *auditdomain.ResourceChangeAuditEvent) error
}
