package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

var (
	ErrInvalidPlan           = errors.New("invalid plan")
	ErrPlanRevisionConflict  = errors.New("plan revision conflict")
	ErrPlanBudgetExceeded    = errors.New("plan budget exceeded")
	ErrInvalidPlanTransition = errors.New("invalid plan transition")
)

type PlanStatus string

const (
	PlanStatusActive          PlanStatus = "active"
	PlanStatusRevising        PlanStatus = "revising"
	PlanStatusWaitingApproval PlanStatus = "waiting_approval"
	PlanStatusBlocked         PlanStatus = "blocked"
	PlanStatusCompleted       PlanStatus = "completed"
	PlanStatusFailed          PlanStatus = "failed"
	PlanStatusCancelled       PlanStatus = "cancelled"
)

type PlanNodeStatus string

const (
	PlanNodeStatusPending                   PlanNodeStatus = "pending"
	PlanNodeStatusRunning                   PlanNodeStatus = "running"
	PlanNodeStatusSucceeded                 PlanNodeStatus = "succeeded"
	PlanNodeStatusFailed                    PlanNodeStatus = "failed"
	PlanNodeStatusBlocked                   PlanNodeStatus = "blocked"
	PlanNodeStatusCancelled                 PlanNodeStatus = "cancelled"
	PlanNodeStatusFailedPendingConfirmation PlanNodeStatus = "failed_pending_confirmation"
)

type Plan struct {
	ID       string     `json:"id"`
	Revision int64      `json:"revision"`
	Status   PlanStatus `json:"status"`
	Nodes    []PlanNode `json:"nodes"`
}

type PlanNode struct {
	ID        string         `json:"id"`
	Goal      string         `json:"goal"`
	HintTools []string       `json:"hint_tools,omitempty"`
	DependsOn []string       `json:"depends_on,omitempty"`
	Status    PlanNodeStatus `json:"status"`
	Attempts  []PlanAttempt  `json:"attempts,omitempty"`
}

type PlanAttempt struct {
	ID      string `json:"id"`
	Number  int    `json:"number"`
	Summary string `json:"summary,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (n PlanNode) CanRetry(maxAttempts int) bool {
	return n.Status == PlanNodeStatusFailed && len(n.Attempts) < maxAttempts
}

type PlanCommandKind string

const (
	PlanCommandCreate   PlanCommandKind = "create"
	PlanCommandRevise   PlanCommandKind = "revise"
	PlanCommandContinue PlanCommandKind = "continue"
	PlanCommandCancel   PlanCommandKind = "cancel"
)

type PlanCommand struct {
	Kind             PlanCommandKind         `json:"kind"`
	ExpectedRevision int64                   `json:"expected_revision"`
	Nodes            []PlanNodeInput         `json:"nodes,omitempty"`
	Operations       []PlanRevisionOperation `json:"operations,omitempty"`
}

type PlanNodeInput struct {
	Key       string   `json:"key"`
	Goal      string   `json:"goal"`
	HintTools []string `json:"hint_tools,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
}

type PlanOperationKind string

const (
	PlanOperationAdd                 PlanOperationKind = "add"
	PlanOperationUpdate              PlanOperationKind = "update"
	PlanOperationRemove              PlanOperationKind = "remove"
	PlanOperationReplaceDependencies PlanOperationKind = "replace_dependencies"
)

type PlanRevisionOperation struct {
	Kind      PlanOperationKind `json:"kind"`
	NodeID    string            `json:"node_id,omitempty"`
	Key       string            `json:"key,omitempty"`
	Goal      string            `json:"goal,omitempty"`
	HintTools []string          `json:"hint_tools,omitempty"`
	DependsOn []string          `json:"depends_on,omitempty"`
}

type PlanLimits struct {
	MaxNodes           int
	MaxRevisions       int64
	MaxAttemptsPerNode int
}

// ApplyPlanCommand validates and applies a model-proposed plan command to a
// clone. Runtime-owned identifiers are supplied by newID.
func ApplyPlanCommand(current *Plan, command PlanCommand, newID func() string, limits PlanLimits) (*Plan, error) {
	if newID == nil {
		return nil, fmt.Errorf("%w: ID source is required", ErrInvalidPlan)
	}
	if command.Kind == PlanCommandCreate {
		return createPlan(current, command, newID, limits)
	}
	if current == nil {
		return nil, fmt.Errorf("%w: no active plan", ErrInvalidPlanTransition)
	}
	if command.ExpectedRevision != current.Revision {
		return nil, fmt.Errorf("%w: expected %d, current %d", ErrPlanRevisionConflict, command.ExpectedRevision, current.Revision)
	}
	if terminalPlanStatus(current.Status) {
		return nil, fmt.Errorf("%w: plan is %s", ErrInvalidPlanTransition, current.Status)
	}
	if limits.MaxRevisions > 0 && current.Revision >= limits.MaxRevisions {
		return nil, fmt.Errorf("%w: maximum revisions reached", ErrPlanBudgetExceeded)
	}

	next := clonePlan(current)
	switch command.Kind {
	case PlanCommandRevise:
		next.Status = PlanStatusRevising
		if err := applyRevision(next, command.Operations, newID, limits); err != nil {
			return nil, err
		}
		next.Status = PlanStatusActive
	case PlanCommandContinue:
		if next.Status != PlanStatusActive && next.Status != PlanStatusBlocked {
			return nil, fmt.Errorf("%w: cannot continue from %s", ErrInvalidPlanTransition, next.Status)
		}
		next.Status = PlanStatusActive
	case PlanCommandCancel:
		next.Status = PlanStatusCancelled
		for index := range next.Nodes {
			if !terminalNodeStatus(next.Nodes[index].Status) {
				next.Nodes[index].Status = PlanNodeStatusCancelled
			}
		}
	default:
		return nil, fmt.Errorf("%w: unknown command %q", ErrInvalidPlan, command.Kind)
	}
	next.Revision++
	return next, nil
}

func createPlan(current *Plan, command PlanCommand, newID func() string, limits PlanLimits) (*Plan, error) {
	if current != nil {
		return nil, fmt.Errorf("%w: plan already exists", ErrInvalidPlanTransition)
	}
	if command.ExpectedRevision != 0 {
		return nil, fmt.Errorf("%w: create expects revision zero", ErrPlanRevisionConflict)
	}
	if len(command.Nodes) == 0 {
		return nil, fmt.Errorf("%w: plan has no nodes", ErrInvalidPlan)
	}
	if limits.MaxNodes > 0 && len(command.Nodes) > limits.MaxNodes {
		return nil, fmt.Errorf("%w: node count %d", ErrPlanBudgetExceeded, len(command.Nodes))
	}
	plan := &Plan{ID: newID(), Revision: 1, Status: PlanStatusActive}
	keyIDs := make(map[string]string, len(command.Nodes))
	for _, input := range command.Nodes {
		if strings.TrimSpace(input.Key) == "" || strings.TrimSpace(input.Goal) == "" {
			return nil, fmt.Errorf("%w: node key and goal are required", ErrInvalidPlan)
		}
		if _, exists := keyIDs[input.Key]; exists {
			return nil, fmt.Errorf("%w: duplicate node key %q", ErrInvalidPlan, input.Key)
		}
		id := newID()
		keyIDs[input.Key] = id
		plan.Nodes = append(plan.Nodes, PlanNode{
			ID: id, Goal: strings.TrimSpace(input.Goal), HintTools: slices.Clone(input.HintTools), Status: PlanNodeStatusPending,
		})
	}
	for index, input := range command.Nodes {
		for _, dependencyKey := range input.DependsOn {
			dependencyID, exists := keyIDs[dependencyKey]
			if !exists {
				return nil, fmt.Errorf("%w: unknown dependency key %q", ErrInvalidPlan, dependencyKey)
			}
			plan.Nodes[index].DependsOn = append(plan.Nodes[index].DependsOn, dependencyID)
		}
	}
	if err := validatePlanNodes(plan.Nodes); err != nil {
		return nil, err
	}
	return plan, nil
}

func applyRevision(plan *Plan, operations []PlanRevisionOperation, newID func() string, limits PlanLimits) error {
	if len(operations) == 0 {
		return fmt.Errorf("%w: revision has no operations", ErrInvalidPlan)
	}
	for _, operation := range operations {
		switch operation.Kind {
		case PlanOperationAdd:
			if strings.TrimSpace(operation.Goal) == "" {
				return fmt.Errorf("%w: added node goal is required", ErrInvalidPlan)
			}
			plan.Nodes = append(plan.Nodes, PlanNode{
				ID: newID(), Goal: strings.TrimSpace(operation.Goal), HintTools: slices.Clone(operation.HintTools),
				DependsOn: slices.Clone(operation.DependsOn), Status: PlanNodeStatusPending,
			})
		case PlanOperationUpdate:
			node, err := mutablePlanNode(plan, operation.NodeID)
			if err != nil {
				return err
			}
			if strings.TrimSpace(operation.Goal) == "" {
				return fmt.Errorf("%w: updated node goal is required", ErrInvalidPlan)
			}
			node.Goal = strings.TrimSpace(operation.Goal)
			node.HintTools = slices.Clone(operation.HintTools)
		case PlanOperationRemove:
			if err := removePlanNode(plan, operation.NodeID); err != nil {
				return err
			}
		case PlanOperationReplaceDependencies:
			node, err := mutablePlanNode(plan, operation.NodeID)
			if err != nil {
				return err
			}
			node.DependsOn = slices.Clone(operation.DependsOn)
		default:
			return fmt.Errorf("%w: unknown revision operation %q", ErrInvalidPlan, operation.Kind)
		}
		if limits.MaxNodes > 0 && len(plan.Nodes) > limits.MaxNodes {
			return fmt.Errorf("%w: node count %d", ErrPlanBudgetExceeded, len(plan.Nodes))
		}
	}
	return validatePlanNodes(plan.Nodes)
}

func mutablePlanNode(plan *Plan, id string) (*PlanNode, error) {
	for index := range plan.Nodes {
		if plan.Nodes[index].ID != id {
			continue
		}
		if plan.Nodes[index].Status != PlanNodeStatusPending && plan.Nodes[index].Status != PlanNodeStatusFailed {
			return nil, fmt.Errorf("%w: node %q is %s", ErrInvalidPlanTransition, id, plan.Nodes[index].Status)
		}
		return &plan.Nodes[index], nil
	}
	return nil, fmt.Errorf("%w: node %q not found", ErrInvalidPlan, id)
}

func removePlanNode(plan *Plan, id string) error {
	if _, err := mutablePlanNode(plan, id); err != nil {
		return err
	}
	for _, node := range plan.Nodes {
		if slices.Contains(node.DependsOn, id) {
			return fmt.Errorf("%w: node %q is still required by %q", ErrInvalidPlan, id, node.ID)
		}
	}
	for index := range plan.Nodes {
		if plan.Nodes[index].ID == id {
			plan.Nodes = append(plan.Nodes[:index], plan.Nodes[index+1:]...)
			return nil
		}
	}
	return nil
}

func validatePlanNodes(nodes []PlanNode) error {
	byID := make(map[string]PlanNode, len(nodes))
	for _, node := range nodes {
		if node.ID == "" || strings.TrimSpace(node.Goal) == "" {
			return fmt.Errorf("%w: node ID and goal are required", ErrInvalidPlan)
		}
		if _, exists := byID[node.ID]; exists {
			return fmt.Errorf("%w: duplicate node %q", ErrInvalidPlan, node.ID)
		}
		byID[node.ID] = node
	}
	visiting := make(map[string]bool, len(nodes))
	visited := make(map[string]bool, len(nodes))
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("%w: dependency cycle", ErrInvalidPlan)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		seen := make(map[string]bool, len(byID[id].DependsOn))
		for _, dependency := range byID[id].DependsOn {
			if _, exists := byID[dependency]; !exists {
				return fmt.Errorf("%w: node %q depends on missing node %q", ErrInvalidPlan, id, dependency)
			}
			if seen[dependency] {
				return fmt.Errorf("%w: node %q repeats dependency %q", ErrInvalidPlan, id, dependency)
			}
			seen[dependency] = true
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for id := range byID {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func clonePlan(plan *Plan) *Plan {
	next := *plan
	next.Nodes = slices.Clone(plan.Nodes)
	for index := range next.Nodes {
		next.Nodes[index].HintTools = slices.Clone(plan.Nodes[index].HintTools)
		next.Nodes[index].DependsOn = slices.Clone(plan.Nodes[index].DependsOn)
		next.Nodes[index].Attempts = slices.Clone(plan.Nodes[index].Attempts)
	}
	return &next
}

func terminalPlanStatus(status PlanStatus) bool {
	return status == PlanStatusCompleted || status == PlanStatusFailed || status == PlanStatusCancelled
}

func terminalNodeStatus(status PlanNodeStatus) bool {
	return status == PlanNodeStatusSucceeded || status == PlanNodeStatusFailed || status == PlanNodeStatusBlocked ||
		status == PlanNodeStatusCancelled || status == PlanNodeStatusFailedPendingConfirmation
}
