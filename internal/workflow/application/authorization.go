package application

import "github.com/byteBuilderX/stratum/internal/workflow/domain"

type Actor struct {
	UserID string
	Role   string
}

type RunAction string

const (
	RunActionRead          RunAction = "read"
	RunActionEvents        RunAction = "events"
	RunActionCancel        RunAction = "cancel"
	RunActionPause         RunAction = "pause"
	RunActionResume        RunAction = "resume"
	RunActionApprove       RunAction = "approve"
	RunActionResolveManual RunAction = "resolve_manual"
)

func authorizeRun(run *domain.Run, actor Actor, action RunAction) error {
	if run == nil || actor.UserID == "" {
		return domain.ErrForbidden
	}
	if actor.Role == "admin" || actor.Role == "owner" {
		return nil
	}
	if run.CreatedBy != actor.UserID {
		return domain.ErrNotFound
	}
	switch action {
	case RunActionRead, RunActionEvents, RunActionCancel:
		return nil
	default:
		return domain.ErrForbidden
	}
}
