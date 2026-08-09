package dto

import (
	"time"

	scheddomain "github.com/byteBuilderX/stratum/internal/scheduler/domain"
)

// CreateScheduledTaskRequest creates an enabled scheduled task bound to a
// workflow version. Cron validation and input-schema conformance are checked
// by the service so bad schedules surface as 400s before any row is written.
type CreateScheduledTaskRequest struct {
	Name          string         `json:"name" binding:"required"`
	WorkflowID    string         `json:"workflowId" binding:"required"`
	VersionID     string         `json:"versionId" binding:"required"`
	InputTemplate map[string]any `json:"inputTemplate"`
	CronExpr      string         `json:"cronExpr" binding:"required"`
}

// UpdateScheduledTaskRequest replaces every editable field; the schedule is
// restarted from the freshly computed next fire time.
type UpdateScheduledTaskRequest struct {
	Name          string         `json:"name" binding:"required"`
	WorkflowID    string         `json:"workflowId" binding:"required"`
	VersionID     string         `json:"versionId" binding:"required"`
	InputTemplate map[string]any `json:"inputTemplate"`
	CronExpr      string         `json:"cronExpr" binding:"required"`
}

// SetScheduledTaskEnabledRequest toggles the schedule. Re-enabling recomputes
// next_fire_at from now; disabling keeps the row but removes it from the due
// set.
//
// 注意：不能加 binding:"required"——bool 的零值 false 会被 required 拒绝。
type SetScheduledTaskEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

// ScheduledTaskResponse is the admin/member surface of one scheduled task.
type ScheduledTaskResponse struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	WorkflowID       string         `json:"workflowId"`
	VersionID        string         `json:"versionId"`
	InputTemplate    map[string]any `json:"inputTemplate"`
	CronExpr         string         `json:"cronExpr"`
	Enabled          bool           `json:"enabled"`
	NextFireAt       time.Time      `json:"nextFireAt"`
	LastRunAt        *time.Time     `json:"lastRunAt,omitempty"`
	LastRunStatus    string         `json:"lastRunStatus"`
	LastErrorMessage string         `json:"lastErrorMessage,omitempty"`
	CreatedBy        string         `json:"createdBy"`
	CreatedAt        time.Time      `json:"createdAt"`
	UpdatedAt        time.Time      `json:"updatedAt"`
}

// ToScheduledTaskResponse maps a scheduled task to its HTTP surface.
func ToScheduledTaskResponse(t scheddomain.ScheduledTask) ScheduledTaskResponse {
	return ScheduledTaskResponse{
		ID:               t.ID,
		Name:             t.Name,
		WorkflowID:       t.WorkflowID,
		VersionID:        t.VersionID,
		InputTemplate:    t.InputTemplate,
		CronExpr:         t.CronExpr,
		Enabled:          t.Enabled,
		NextFireAt:       t.NextFireAt,
		LastRunAt:        t.LastRunAt,
		LastRunStatus:    t.LastRunStatus,
		LastErrorMessage: t.LastErrorMessage,
		CreatedBy:        t.CreatedBy,
		CreatedAt:        t.CreatedAt,
		UpdatedAt:        t.UpdatedAt,
	}
}

// ScheduledTaskPageResponse paginates the task list newest-first.
type ScheduledTaskPageResponse struct {
	Tasks    []ScheduledTaskResponse `json:"tasks"`
	Total    int                     `json:"total"`
	Page     int                     `json:"page"`
	PageSize int                     `json:"pageSize"`
}
