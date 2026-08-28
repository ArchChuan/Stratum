// Package domain defines the scheduled-task bounded context types.
// Cron parsing lives in the application layer: the domain only carries the
// expression string plus the already-computed next fire time.
package domain

import (
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// Last-run statuses persisted in scheduled_tasks.last_run_status.
const (
	LastRunOK    = "ok"
	LastRunError = "error"
)

// ScheduledTask is a tenant-owned cron-triggered workflow invocation.
type ScheduledTask struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	WorkflowID       string         `json:"workflow_id"`
	VersionID        string         `json:"version_id"`
	InputTemplate    map[string]any `json:"input_template"`
	CronExpr         string         `json:"cron_expr"`
	Enabled          bool           `json:"enabled"`
	NextFireAt       time.Time      `json:"next_fire_at"`
	LastRunAt        *time.Time     `json:"last_run_at,omitempty"`
	LastRunStatus    string         `json:"last_run_status,omitempty"`
	LastErrorMessage string         `json:"last_error_message,omitempty"`
	CreatedBy        string         `json:"created_by"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	// 展示用引用实体名称（默认展示 name，原始 id 可 hover 悬浮）：
	// WorkflowName/VersionNo/VersionName 由 application 层在列表/详情时解析填充，
	// 不在 NewScheduledTask 中校验；版本被删除时保持零值，调用方回退展示原始 ID。
	WorkflowName string `json:"workflow_name,omitempty"`
	VersionNo    int64  `json:"version_no,omitempty"`
	VersionName  string `json:"version_name,omitempty"`
}

// NewScheduledTask validates the business invariants and constructs a task.
// CreatedAt/UpdatedAt are left zero; the application layer stamps them at
// persist time. nextFireAt must come from the application's cron parser.
func NewScheduledTask(id, name, workflowID, versionID string, inputTemplate map[string]any, cronExpr string, nextFireAt time.Time) (*ScheduledTask, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", ErrScheduledTaskInvalidInput)
	}
	if n := utf8.RuneCountInString(name); n == 0 || n > constants.MaxScheduledTaskNameRunes {
		return nil, fmt.Errorf("%w: name must be 1-%d runes", ErrScheduledTaskInvalidInput, constants.MaxScheduledTaskNameRunes)
	}
	if workflowID == "" || versionID == "" {
		return nil, fmt.Errorf("%w: workflow and version are required", ErrScheduledTaskInvalidInput)
	}
	if inputTemplate == nil {
		return nil, fmt.Errorf("%w: input template must not be nil", ErrScheduledTaskInvalidInput)
	}
	if len(cronExpr) == 0 || len(cronExpr) > constants.MaxCronExprLen {
		return nil, fmt.Errorf("%w: cron expression must be 1-%d characters", ErrScheduledTaskInvalidInput, constants.MaxCronExprLen)
	}
	if nextFireAt.IsZero() {
		return nil, fmt.Errorf("%w: next fire time is required", ErrScheduledTaskInvalidInput)
	}
	return &ScheduledTask{
		ID:            id,
		Name:          name,
		WorkflowID:    workflowID,
		VersionID:     versionID,
		InputTemplate: inputTemplate,
		CronExpr:      cronExpr,
		Enabled:       true,
		NextFireAt:    nextFireAt,
	}, nil
}
