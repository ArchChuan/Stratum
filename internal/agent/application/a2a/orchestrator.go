// Package a2a provides agent-to-agent communication and orchestration.
package a2a

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// CollaborationStrategy defines how agents collaborate
type CollaborationStrategy string

const (
	StrategySequential   CollaborationStrategy = "sequential"
	StrategyParallel     CollaborationStrategy = "parallel"
	StrategyHierarchical CollaborationStrategy = "hierarchical"
	StrategyConsensus    CollaborationStrategy = "consensus"
	StrategySwarm        CollaborationStrategy = "swarm"
	StrategyPipeline     CollaborationStrategy = "pipeline"
	StrategyVoting       CollaborationStrategy = "voting"
	StrategyAdaptive     CollaborationStrategy = "adaptive"
)

// TaskStep represents a step in a collaboration workflow
type TaskStep struct {
	ID           string
	Name         string
	AgentID      string
	Dependencies []string
	Input        map[string]interface{}
	Output       map[string]interface{}
	Status       string
	StartedAt    time.Time
	CompletedAt  time.Time
	Error        error
	mu           sync.RWMutex
}

// ExecutionPlan defines how a collaboration will be executed
type ExecutionPlan struct {
	ID              string
	CollaborationID string
	TaskDescription string
	Strategy        CollaborationStrategy
	Steps           []*TaskStep
	CurrentStep     int
	Status          string
	CreatedAt       time.Time
	StartedAt       time.Time
	CompletedAt     time.Time
	mu              sync.RWMutex
}

// SharedContext holds shared data for collaboration
type SharedContext struct {
	Data    map[string]interface{}
	Version int
	mu      sync.RWMutex
}

// Orchestrator manages multi-agent collaboration
type Orchestrator struct {
	plans    map[string]*ExecutionPlan
	contexts map[string]*SharedContext
	mu       sync.RWMutex
	logger   *zap.Logger
}

// NewOrchestrator creates a new orchestrator
func NewOrchestrator(logger *zap.Logger) *Orchestrator {
	return &Orchestrator{
		plans:    make(map[string]*ExecutionPlan),
		contexts: make(map[string]*SharedContext),
		logger:   logger.Named("orchestrator"),
	}
}

// CreatePlan creates an execution plan for collaboration
func (o *Orchestrator) CreatePlan(ctx context.Context, collaborationID string, taskDescription string, strategy CollaborationStrategy, participants []AgentIdentity) (*ExecutionPlan, error) {
	plan := &ExecutionPlan{
		ID:              generateMessageID(),
		CollaborationID: collaborationID,
		TaskDescription: taskDescription,
		Strategy:        strategy,
		Steps:           o.createSteps(strategy, participants),
		CurrentStep:     0,
		Status:          "created",
		CreatedAt:       time.Now(),
	}

	o.mu.Lock()
	o.plans[plan.ID] = plan
	o.contexts[collaborationID] = &SharedContext{
		Data:    make(map[string]interface{}),
		Version: 0,
	}
	o.mu.Unlock()

	o.logger.Info("execution plan created",
		zap.String("plan_id", plan.ID),
		zap.String("collaboration_id", collaborationID),
		zap.String("strategy", string(strategy)),
		zap.Int("steps", len(plan.Steps)))

	return plan, nil
}

// createSteps creates task steps based on collaboration strategy
func (o *Orchestrator) createSteps(strategy CollaborationStrategy, participants []AgentIdentity) []*TaskStep {
	switch strategy {
	case StrategySequential:
		return o.createSequentialSteps(participants)
	case StrategyParallel:
		return o.createParallelSteps(participants)
	case StrategyHierarchical:
		return o.createHierarchicalSteps(participants)
	case StrategyPipeline:
		return o.createPipelineSteps(participants)
	case StrategySwarm:
		return o.createSwarmSteps(participants)
	default:
		return o.createSequentialSteps(participants)
	}
}

// createSequentialSteps creates steps where each agent depends on the previous
func (o *Orchestrator) createSequentialSteps(participants []AgentIdentity) []*TaskStep {
	steps := make([]*TaskStep, len(participants))
	for i, agent := range participants {
		dependencies := []string{}
		if i > 0 {
			dependencies = append(dependencies, steps[i-1].ID)
		}
		steps[i] = &TaskStep{
			ID:           generateMessageID(),
			Name:         agent.Name,
			AgentID:      agent.ID,
			Dependencies: dependencies,
			Input:        make(map[string]interface{}),
			Output:       make(map[string]interface{}),
			Status:       "pending",
		}
	}
	return steps
}

// createParallelSteps creates steps that can run in parallel
func (o *Orchestrator) createParallelSteps(participants []AgentIdentity) []*TaskStep {
	steps := make([]*TaskStep, len(participants))
	for i, agent := range participants {
		steps[i] = &TaskStep{
			ID:           generateMessageID(),
			Name:         agent.Name,
			AgentID:      agent.ID,
			Dependencies: []string{},
			Input:        make(map[string]interface{}),
			Output:       make(map[string]interface{}),
			Status:       "pending",
		}
	}
	return steps
}

// createHierarchicalSteps creates a tree structure
func (o *Orchestrator) createHierarchicalSteps(participants []AgentIdentity) []*TaskStep {
	if len(participants) == 0 {
		return []*TaskStep{}
	}

	steps := make([]*TaskStep, 0, len(participants))

	// First agent is the coordinator
	steps = append(steps, &TaskStep{
		ID:           generateMessageID(),
		Name:         participants[0].Name,
		AgentID:      participants[0].ID,
		Dependencies: []string{},
		Input:        make(map[string]interface{}),
		Output:       make(map[string]interface{}),
		Status:       "pending",
	})

	// Other agents depend on coordinator
	for _, agent := range participants[1:] {
		steps = append(steps, &TaskStep{
			ID:           generateMessageID(),
			Name:         agent.Name,
			AgentID:      agent.ID,
			Dependencies: []string{steps[0].ID},
			Input:        make(map[string]interface{}),
			Output:       make(map[string]interface{}),
			Status:       "pending",
		})
	}

	return steps
}

// createPipelineSteps creates a linear pipeline
func (o *Orchestrator) createPipelineSteps(participants []AgentIdentity) []*TaskStep {
	return o.createSequentialSteps(participants)
}

// createSwarmSteps creates independent steps for swarm intelligence
func (o *Orchestrator) createSwarmSteps(participants []AgentIdentity) []*TaskStep {
	steps := make([]*TaskStep, len(participants))
	for i, agent := range participants {
		steps[i] = &TaskStep{
			ID:           generateMessageID(),
			Name:         agent.Name + "_swarm_agent",
			AgentID:      agent.ID,
			Dependencies: []string{},
			Input:        make(map[string]interface{}),
			Output:       make(map[string]interface{}),
			Status:       "pending",
		}
	}
	return steps
}

// GetPlan retrieves an execution plan
func (o *Orchestrator) GetPlan(planID string) (*ExecutionPlan, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	plan, exists := o.plans[planID]
	if !exists {
		return nil, ErrPlanNotFound
	}

	return plan, nil
}

// GetSharedContext retrieves shared context
func (o *Orchestrator) GetSharedContext(collaborationID string) (*SharedContext, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	ctx, exists := o.contexts[collaborationID]
	if !exists {
		return nil, ErrContextNotFound
	}

	return ctx, nil
}

// UpdateContext updates shared context
func (o *Orchestrator) UpdateContext(collaborationID string, key string, value interface{}) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if ctx, exists := o.contexts[collaborationID]; exists {
		ctx.mu.Lock()
		ctx.Data[key] = value
		ctx.Version++
		ctx.mu.Unlock()
	}
}

// MarkStepComplete marks a step as complete
func (o *Orchestrator) MarkStepComplete(planID, stepID string, output map[string]interface{}) error {
	o.mu.RLock()
	plan, exists := o.plans[planID]
	o.mu.RUnlock()

	if !exists {
		return ErrPlanNotFound
	}

	plan.mu.Lock()
	defer plan.mu.Unlock()

	for _, step := range plan.Steps {
		if step.ID == stepID {
			step.mu.Lock()
			step.Status = "completed"
			step.Output = output
			step.CompletedAt = time.Now()
			step.mu.Unlock()
			return nil
		}
	}

	return ErrStepNotFound
}

// Cleanup removes old plans
func (o *Orchestrator) Cleanup(maxAge time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()

	now := time.Now()
	for id, plan := range o.plans {
		plan.mu.RLock()
		old := now.Sub(plan.CreatedAt) > maxAge
		completed := plan.Status == "completed" || plan.Status == "failed"
		plan.mu.RUnlock()

		if old && completed {
			delete(o.plans, id)
			if ctx, exists := o.contexts[plan.CollaborationID]; exists {
				o.contexts[plan.CollaborationID] = nil
				_ = ctx // Use ctx to avoid unused error
			}
		}
	}
}

var (
	ErrPlanNotFound    = &A2AError{Type: ErrorTypeOrchestration, Message: "plan not found"}
	ErrContextNotFound = &A2AError{Type: ErrorTypeOrchestration, Message: "context not found"}
	ErrStepNotFound    = &A2AError{Type: ErrorTypeOrchestration, Message: "step not found"}
)
