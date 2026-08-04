package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/collab/domain"
	"github.com/byteBuilderX/stratum/internal/collab/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// Actor identifies the caller of a collaboration operation.
type Actor struct {
	UserID string
	Role   string
}

// CollaborationService orchestrates plan lifecycle: create / read / start /
// cancel, with actor authorization and input validation.
type CollaborationService struct {
	collabs port.CollaborationRepo
	steps   port.TaskStepRepo
	metrics observability.MetricsProvider
	newID   func() string
	now     func() time.Time
}

// NewCollaborationService wires the collab application service.
func NewCollaborationService(collabs port.CollaborationRepo, steps port.TaskStepRepo, metrics observability.MetricsProvider, newID func() string) *CollaborationService {
	return &CollaborationService{collabs: collabs, steps: steps, metrics: metrics, newID: newID, now: time.Now}
}

// Create validates input and persists a plan in created state.
func (s *CollaborationService) Create(ctx context.Context, tenantID string, actor Actor, desc string, strategy domain.CollabStrategy, participants []string) (*domain.Collaboration, error) {
	if err := validateCreate(desc, strategy, participants); err != nil {
		return nil, err
	}
	collab := &domain.Collaboration{
		ID:              s.newID(),
		TenantID:        tenantID,
		TaskDescription: desc,
		Strategy:        strategy,
		Status:          domain.CollabCreated,
		CreatedBy:       actor.UserID,
		Participants:    dedupe(participants),
		CreatedAt:       s.now(),
	}
	if err := s.collabs.Insert(ctx, *collab); err != nil {
		return nil, fmt.Errorf("collab create: %w", err)
	}
	s.metrics.IncCollabPlan(string(strategy), "created")
	return collab, nil
}

// Get returns a plan to any member actor.
func (s *CollaborationService) Get(ctx context.Context, tenantID, id string, actor Actor) (*domain.Collaboration, error) {
	if actor.UserID == "" {
		return nil, domain.ErrCollabForbidden
	}
	collab, err := s.collabs.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if collab == nil {
		return nil, domain.ErrCollabNotFound
	}
	return collab, nil
}

// List returns plans for the tenant, newest first.
func (s *CollaborationService) List(ctx context.Context, tenantID string, actor Actor, limit, offset int) ([]domain.Collaboration, error) {
	if actor.UserID == "" {
		return nil, domain.ErrCollabForbidden
	}
	if limit <= 0 || limit > constants.MaxPageSize {
		limit = constants.DefaultPageSize
	}
	if offset < 0 {
		offset = 0
	}
	return s.collabs.ListByTenant(ctx, tenantID, limit, offset)
}

// ReadyTasks returns steps whose dependencies are satisfied (for detail view).
func (s *CollaborationService) ReadyTasks(ctx context.Context, tenantID, planID string, actor Actor) ([]domain.TaskStep, error) {
	if actor.UserID == "" {
		return nil, domain.ErrCollabForbidden
	}
	return s.steps.GetReadyTasks(ctx, tenantID, planID)
}

// Start authorizes, migrates the plan to running, and generates the task DAG.
func (s *CollaborationService) Start(ctx context.Context, tenantID, id string, actor Actor) (*domain.Collaboration, error) {
	collab, err := s.collabs.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if collab == nil {
		return nil, domain.ErrCollabNotFound
	}
	if err := authorizeControl(collab, actor); err != nil {
		return nil, err
	}
	now := s.now()
	if err := collab.Start(now); err != nil {
		return nil, err
	}
	steps := generateSteps(collab.ID, collab.Participants, collab.Strategy, collab.TaskDescription, now, s.newID)
	if err := s.steps.InsertBatch(ctx, tenantID, steps); err != nil {
		return nil, fmt.Errorf("collab start steps: %w", err)
	}
	if err := s.collabs.UpdateStatus(ctx, tenantID, collab.ID, collab.Status, collab.StartedAt, nil); err != nil {
		return nil, fmt.Errorf("collab start status: %w", err)
	}
	return collab, nil
}

// Cancel authorizes and moves a created|running plan to canceled, releasing
// pending steps so the worker will not claim them.
func (s *CollaborationService) Cancel(ctx context.Context, tenantID, id string, actor Actor) error {
	collab, err := s.collabs.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if collab == nil {
		return domain.ErrCollabNotFound
	}
	if err := authorizeControl(collab, actor); err != nil {
		return err
	}
	now := s.now()
	if err := collab.Cancel(now); err != nil {
		return err
	}
	if err := s.steps.CancelPending(ctx, tenantID, collab.ID); err != nil {
		return fmt.Errorf("collab cancel steps: %w", err)
	}
	if err := s.collabs.UpdateStatus(ctx, tenantID, collab.ID, collab.Status, nil, collab.CompletedAt); err != nil {
		return fmt.Errorf("collab cancel status: %w", err)
	}
	return nil
}

// authorizeControl gates control actions: admin/owner bypass; otherwise only
// the plan creator, hidden as not-found to prevent enumeration.
func authorizeControl(collab *domain.Collaboration, actor Actor) error {
	if collab == nil || actor.UserID == "" {
		return domain.ErrCollabForbidden
	}
	if actor.Role == "admin" || actor.Role == "owner" {
		return nil
	}
	if collab.CreatedBy != actor.UserID {
		return domain.ErrCollabNotFound
	}
	return nil
}

func validateCreate(desc string, strategy domain.CollabStrategy, participants []string) error {
	if strings.TrimSpace(desc) == "" {
		return fmt.Errorf("%w: task_description is required", domain.ErrCollabInvalidInput)
	}
	if len([]rune(desc)) > TaskDescriptionMaxRunes {
		return fmt.Errorf("%w: task_description exceeds %d runes", domain.ErrCollabInvalidInput, TaskDescriptionMaxRunes)
	}
	switch strategy {
	case domain.CollabSequential, domain.CollabParallel, domain.CollabSwarm, domain.CollabPipeline, domain.CollabHierarchical:
	default:
		return fmt.Errorf("%w: unknown strategy %q", domain.ErrCollabInvalidInput, strategy)
	}
	if len(participants) == 0 {
		return fmt.Errorf("%w: participants are required", domain.ErrCollabInvalidInput)
	}
	if len(participants) > MaxCollabParticipants {
		return fmt.Errorf("%w: participants exceed %d", domain.ErrCollabInvalidInput, MaxCollabParticipants)
	}
	for _, p := range participants {
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("%w: participants must not contain empty ids", domain.ErrCollabInvalidInput)
		}
	}
	return nil
}

// dedupe removes duplicate participant ids preserving first-occurrence order.
func dedupe(participants []string) []string {
	seen := make(map[string]struct{}, len(participants))
	out := make([]string, 0, len(participants))
	for _, p := range participants {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
