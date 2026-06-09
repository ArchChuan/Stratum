// Package agent provides the core agent system.
package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	agentworkflow "github.com/byteBuilderX/ClawHermes-AI-Go/internal/agent/workflow"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/capgateway"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/memory"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/observability"
	temporalclient "go.temporal.io/sdk/client"
	"go.uber.org/zap"
)

// TemporalWorkflowStarter is a minimal interface over temporal client for testability.
type TemporalWorkflowStarter interface {
	ExecuteWorkflow(ctx context.Context, options temporalclient.StartWorkflowOptions, workflow interface{}, args ...interface{}) (temporalclient.WorkflowRun, error)
}

// AgentType defines different agent architectures
type AgentType string

const (
	ReActAgent       AgentType = "react"
	CoTAgent         AgentType = "cot"
	PlanningAgent    AgentType = "planning"
	ToolCallingAgent AgentType = "tool_calling"
	RAGAgent         AgentType = "rag"
	SwarmAgent       AgentType = "swarm"
)

// AgentCapability defines what an agent can do
type AgentCapability struct {
	Name        string
	Description string
	CanUseTools bool
	CanPlan     bool
	CanReason   bool
}

// Message represents a message in agent's conversation history
type Message struct {
	Role       string
	Content    string
	Timestamp  time.Time
	Metadata   map[string]interface{}
	TokenCount int
}

// Thought represents a single reasoning step in CoT
type Thought struct {
	Step        int
	Observation string
	Thought     string
}

// ToolCall represents a structured tool invocation
type ToolCall struct {
	ToolName string
	Input    map[string]interface{}
	Output   interface{}
	Error    error
	Duration time.Duration
}

// ExecutionConfig holds configuration for agent execution
type ExecutionConfig struct {
	MaxSteps       int
	Timeout        time.Duration
	Temperature    float32
	EnableMemory   bool
	EnableTools    bool
	AvailableTools []string
	Stream         bool
}

// AgentConfig holds agent configuration
type AgentConfig struct {
	ID            string
	Name          string
	Type          AgentType
	Description   string
	Persona       string
	SystemPrompt  string
	LLMModel      string
	MaxIterations int
	AllowedSkills []string
	Capabilities  []AgentCapability
}

// AgentResult represents output from an agent execution
type AgentResult struct {
	AgentID    string
	Input      string
	Output     string
	Thoughts   []Thought
	ToolCalls  []ToolCall
	Steps      int
	TokensUsed int
	Duration   time.Duration
	Error      error
	Metadata   map[string]interface{}
}

// Agent defines the interface for all agent types
type Agent interface {
	GetConfig() *AgentConfig
	Execute(ctx context.Context, input string, options ...ExecutionOption) (*AgentResult, error)
	Reset()
	GetMemory() []Message
}

// BaseAgent provides common functionality for all agent implementations
type BaseAgent struct {
	*AgentConfig
	Logger         *zap.Logger
	metrics        observability.MetricsProvider
	State          AgentState
	Memory         []Message
	mu             sync.Mutex
	MemoryManager  *memory.MemoryManager
	SessionContext *memory.SessionContext
	TemporalClient TemporalWorkflowStarter
	CapGateway     capgateway.CapabilityGateway
}

// AgentState represents the current state of an agent
type AgentState struct {
	StepsTaken int
	Thoughts   []Thought
	ToolCalls  []ToolCall
	TokensUsed int
}

// NewBaseAgent creates a new base agent
func NewBaseAgent(config *AgentConfig, logger *zap.Logger) *BaseAgent {
	return &BaseAgent{
		AgentConfig:    config,
		Logger:         logger,
		metrics:        observability.NoopMetrics{},
		State:          AgentState{},
		Memory:         []Message{},
		mu:             sync.Mutex{},
		MemoryManager:  nil,
		SessionContext: nil,
	}
}

// NewBaseAgentWithMemory creates a new base agent with memory support
func NewBaseAgentWithMemory(config *AgentConfig, logger *zap.Logger, memoryManager *memory.MemoryManager, sessionCtx *memory.SessionContext) *BaseAgent {
	return &BaseAgent{
		AgentConfig:    config,
		Logger:         logger,
		metrics:        observability.NoopMetrics{},
		State:          AgentState{},
		Memory:         []Message{},
		mu:             sync.Mutex{},
		MemoryManager:  memoryManager,
		SessionContext: sessionCtx,
	}
}

// WithMetrics injects a MetricsProvider. Must be called before the agent is shared across goroutines.
func (a *BaseAgent) WithMetrics(m observability.MetricsProvider) *BaseAgent {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.metrics = m
	return a
}

// SetMemoryManager sets the memory manager for the agent
func (a *BaseAgent) SetMemoryManager(manager *memory.MemoryManager, sessionCtx *memory.SessionContext) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.MemoryManager = manager
	a.SessionContext = sessionCtx
}

func (a *BaseAgent) SetTemporalClient(c TemporalWorkflowStarter) {
	a.TemporalClient = c
}

func (a *BaseAgent) SetCapGateway(gw capgateway.CapabilityGateway) {
	a.CapGateway = gw
}

// GetConfig implements Agent interface
func (a *BaseAgent) GetConfig() *AgentConfig {
	return a.AgentConfig
}

// Reset implements Agent interface
func (a *BaseAgent) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.State = AgentState{}
	a.Memory = []Message{}
}

// GetMemory returns the agent's conversation memory
func (a *BaseAgent) GetMemory() []Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Memory
}

// AddToMemory adds a message to the agent's memory
func (a *BaseAgent) AddToMemory(msg Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	msg.Timestamp = time.Now()
	a.Memory = append(a.Memory, msg)
	if len(a.Memory) > 100 {
		a.Memory = a.Memory[len(a.Memory)-100:]
	}

	// Also add to memory manager if available
	if a.MemoryManager != nil && a.SessionContext != nil {
		entry := &memory.MemoryEntry{
			Role:      msg.Role,
			Content:   msg.Content,
			TenantID:  a.SessionContext.TenantID,
			UserID:    a.SessionContext.UserID,
			SessionID: a.SessionContext.SessionID,
			AgentID:   a.SessionContext.AgentID,
			Metadata:  msg.Metadata,
		}
		ctx := context.Background()
		if err := a.MemoryManager.Add(ctx, entry); err != nil {
			a.Logger.Warn("failed to add to memory manager", zap.Error(err))
		}
	}
}

// RetrieveMemory retrieves relevant memory entries for context
func (a *BaseAgent) RetrieveMemory(ctx context.Context, query string, limit int) ([]*memory.MemorySearchResult, error) {
	if a.MemoryManager == nil || a.SessionContext == nil {
		return []*memory.MemorySearchResult{}, nil
	}

	searchReq := &memory.MemorySearchRequest{
		Query:   query,
		Context: a.SessionContext,
		Limit:   limit,
	}

	return a.MemoryManager.Search(ctx, searchReq)
}

// Execute implements the Agent interface - base implementation with ReAct pattern
func (a *BaseAgent) Execute(ctx context.Context, input string, options ...ExecutionOption) (*AgentResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	startTime := time.Now()

	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)

	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = a.MaxIterations
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	a.Logger.Info("agent execution started",
		zap.String("agent_id", a.ID),
		zap.String("type", string(a.Type)),
		zap.String("input", input))

	result := &AgentResult{
		AgentID:  a.ID,
		Input:    input,
		Metadata: map[string]interface{}{},
	}

	var execErr error
	switch a.Type {
	case ReActAgent:
		if a.TemporalClient == nil {
			execErr = fmt.Errorf("react: TemporalClient not set")
			break
		}
		wfReq := agentworkflow.ReActRequest{
			TenantID: a.ID,
			AgentID:  a.ID,
			Input:    input,
			AgentCfg: agentworkflow.AgentWorkflowConfig{
				ID:            a.ID,
				Name:          a.Name,
				LLMModel:      a.LLMModel,
				SystemPrompt:  a.SystemPrompt,
				MaxIterations: a.MaxIterations,
			},
		}
		wfOpts := temporalclient.StartWorkflowOptions{
			ID:        fmt.Sprintf("react-%s-%d", a.ID, time.Now().UnixNano()),
			TaskQueue: agentworkflow.TaskQueue,
		}
		run, err := a.TemporalClient.ExecuteWorkflow(ctx, wfOpts, agentworkflow.ReActWorkflow, wfReq)
		if err != nil {
			execErr = fmt.Errorf("react: start workflow: %w", err)
			break
		}
		var wfResult *agentworkflow.ReActResult
		if err := run.Get(ctx, &wfResult); err != nil {
			execErr = fmt.Errorf("react: workflow: %w", err)
			break
		}
		result.Output = wfResult.Output
		result.Steps = wfResult.Steps

	case CoTAgent:
		for i := 0; i < cfg.MaxSteps; i++ {
			thought := Thought{
				Step:        i + 1,
				Observation: "Thinking about: " + input,
				Thought:     "Considering possible responses",
			}
			result.Thoughts = append(result.Thoughts, thought)
			a.State.StepsTaken++

			if i >= 2 {
				result.Output = fmt.Sprintf("Response for: %s", input)
				break
			}
		}

	case PlanningAgent, ToolCallingAgent, RAGAgent, SwarmAgent:
		result.Output = fmt.Sprintf("%s agent type not yet implemented", string(a.Type))
		execErr = fmt.Errorf("agent type %s not implemented", a.Type)

	default:
		result.Output = "Unknown agent type"
		execErr = fmt.Errorf("unknown agent type: %s", a.Type)
	}

	result.Duration = time.Since(startTime)
	result.Steps = a.State.StepsTaken

	status := "success"
	if execErr != nil {
		status = "error"
	}
	a.metrics.IncAgentExecution(a.ID, string(a.Type), status)
	a.metrics.RecordAgentExecutionDuration(a.ID, string(a.Type), result.Duration.Seconds())
	a.metrics.RecordAgentStepCount(a.ID, string(a.Type), result.Steps)

	return result, execErr
}

// ExecutionOption configures agent execution behavior
type ExecutionOption func(*ExecutionConfig)

// WithMaxSteps sets the maximum number of steps
func WithMaxSteps(maxSteps int) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.MaxSteps = maxSteps
	}
}

// WithTimeout sets the execution timeout
func WithTimeout(timeout time.Duration) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.Timeout = timeout
	}
}

// WithTemperature sets the LLM temperature
func WithTemperature(temperature float32) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.Temperature = temperature
	}
}

// WithMemory enables memory usage
func WithMemory(enable bool) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.EnableMemory = enable
	}
}

// WithTools enables tool usage
func WithTools(tools []string) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.AvailableTools = tools
		cfg.EnableTools = true
	}
}

// WithStream enables streaming output
func WithStream(enable bool) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.Stream = enable
	}
}

// ApplyOptions applies options to the execution config
func (cfg *ExecutionConfig) ApplyOptions(opts []ExecutionOption) {
	for _, opt := range opts {
		opt(cfg)
	}
}
