// Package domain holds canonical agent context types and sentinels.
//
// This file is the single source of truth for agent / chat / execution
// data shapes shared across application + infrastructure layers.
// Application keeps thin type aliases (`type X = domain.X`) so existing
// call-sites remain source-compatible after the layering refactor.

package domain

import (
	"encoding/json"
	"time"
)

// AgentType enumerates supported agent architectures.
type AgentType string

const (
	ReActAgent       AgentType = "react"
	CoTAgent         AgentType = "cot"
	PlanningAgent    AgentType = "planning"
	ToolCallingAgent AgentType = "tool_calling"
	RAGAgent         AgentType = "rag"
	SwarmAgent       AgentType = "swarm"
)

// AgentCapability declares what an agent can do.
type AgentCapability struct {
	Name        string
	Description string
	CanUseTools bool
	CanPlan     bool
	CanReason   bool
}

// AgentConfig holds the persisted shape of an agent.
type AgentConfig struct {
	ID                             string
	Name                           string
	Type                           AgentType
	Description                    string
	SystemPrompt                   string
	LLMModel                       string
	EmbedModel                     string
	MaxIterations                  int
	AllowedSkills                  []string
	MCPToolIDs                     []string
	Capabilities                   []AgentCapability
	KnowledgeWorkspaceIDs          []string
	KnowledgeWorkspaceNames        []string
	KnowledgeWorkspaceDescriptions []string
	MaxContextTokens               int
	MemoryScope                    string
	SystemKey                      string
	IsSystem                       bool
	ManagementMode                 string
	// StuckThreshold > 0 enables lazy planning: after this many LLM rounds with
	// no final answer the agent transitions to Reflect→Plan→Execute.
	// 0 disables the feature (pure ReAct).
	StuckThreshold    int
	CheckpointEnabled bool
}

// PlanStep is a single goal inside an agent execution plan.
type PlanStep struct {
	Goal      string   `json:"goal"`
	HintTools []string `json:"hint_tools,omitempty"`
	// DependsOn lists the zero-based indices of steps that must complete before
	// this step starts. Empty means the step can run in the first wave (parallel).
	DependsOn []int `json:"depends_on,omitempty"`
}

// StepResult captures the outcome of executing one PlanStep.
type StepResult struct {
	StepIndex int    `json:"step_index"`
	Goal      string `json:"goal"`
	Summary   string `json:"summary"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
}

// PlanRuntimeState is serialised into AgentExecutionCheckpoint.RuntimeStateJSON.
type PlanRuntimeState struct {
	Phase             string       `json:"phase"`
	ReflectionSummary string       `json:"reflection_summary"`
	Plan              []PlanStep   `json:"plan"`
	PlanTemplateID    string       `json:"plan_template_id,omitempty"`
	CurrentStepIndex  int          `json:"current_step_index"`
	StepResults       []StepResult `json:"step_results"`
}

// ChatConversation is a named conversation thread between a user and an agent.
type ChatConversation struct {
	ID        string
	AgentID   string
	UserID    string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
	ExpiresAt time.Time
	DeletedAt *time.Time
}

// ChatMessage is a single message in a conversation.
type ChatMessage struct {
	ID             string
	ConversationID string
	Role           string // "user" | "assistant"
	Content        string
	StepsJSON      json.RawMessage
	IsError        bool
	CreatedAt      time.Time
	UserID         string
	AgentID        string
	MemoryScope    string
	SkipOutbox     bool
}

const (
	ExecStatusSuccess         = "success"
	ExecStatusError           = "error"
	ExecStatusWaitingApproval = "waiting_approval"
)

// ExecutionRecord is an agent execution history entry.
type ExecutionRecord struct {
	ID            string
	TraceID       string
	AgentID       string
	AgentName     string
	UserID        string
	Status        string
	InputPreview  string
	OutputPreview string
	ErrorMessage  string
	TotalTokens   int
	CostUSD       float64
	DurationMs    int
	CreatedAt     time.Time
}

// ListOptions controls pagination for execution history queries.
type ListOptions struct {
	Page     int
	PageSize int
}

// Message represents a single message in an agent's in-memory conversation history.
type Message struct {
	Role       string
	Content    string
	Timestamp  time.Time
	Metadata   map[string]interface{}
	TokenCount int
}

// Thought represents a single reasoning step in Chain-of-Thought execution.
type Thought struct {
	Step        int
	Observation string
	Thought     string
}

// ToolCall represents a structured tool invocation and its result.
type ToolCall struct {
	ToolName string
	Input    map[string]interface{}
	Output   interface{}
	Error    error
	Duration time.Duration
}

const (
	ToolTraceStatusSuccess = "success"
	ToolTraceStatusError   = "error"

	ToolTypeReasoning     = "reasoning"
	ToolTypeBuiltinRAG    = "builtin_rag"
	ToolTypeBuiltinMemory = "builtin_memory"
	ToolTypeSkill         = "skill"
	ToolTypeMCP           = "mcp"
	ToolTypeInternal      = "internal"

	RunTypeAgent         = "agent"
	RunTypeWorkflow      = "workflow"
	RunTypeSkillTest     = "skill_test"
	RunTypeScheduledTask = "scheduled_task"

	ObservationTypeAgent      = "agent"
	ObservationTypeLLM        = "llm"
	ObservationTypeTool       = "tool"
	ObservationTypeMCP        = "mcp"
	ObservationTypeSkill      = "skill"
	ObservationTypeRetriever  = "retriever"
	ObservationTypeMemory     = "memory"
	ObservationTypeWorkflow   = "workflow"
	ObservationTypeCheckpoint = "checkpoint"
	ObservationTypeCustom     = "custom"

	ProviderTypeSkill    = "skill"
	ProviderTypeMCP      = "mcp"
	ProviderTypeLLM      = "llm"
	ProviderTypeBuiltin  = "builtin"
	ProviderTypeInternal = "internal"
	ProviderTypeHTTP     = "http"
	ProviderTypeBrowser  = "browser"
	ProviderTypeShell    = "shell"

	TraceEventAgentStarted  = "agent.execution_started"
	TraceEventLLMRequest    = "llm.request"
	TraceEventLLMResponse   = "llm.response"
	TraceEventToolStarted   = "tool.call_started"
	TraceEventToolFinished  = "tool.call_finished"
	TraceEventToolFailed    = "tool.call_failed"
	TraceEventFinalAnswer   = "agent.final_answer"
	TraceEventAgentFinished = "agent.execution_finished"
	TraceEventAgentFailed   = "agent.execution_failed"
)

// ToolObservation captures a single tool invocation for audit/debug storage
// and for producing a compact context summary for the next conversation turn.
type ToolObservation struct {
	ID             string         `json:"id"`
	TraceID        string         `json:"trace_id"`
	ExecutionID    string         `json:"execution_id"`
	ConversationID string         `json:"conversation_id"`
	AgentID        string         `json:"agent_id"`
	UserID         string         `json:"user_id"`
	StepIndex      int            `json:"step_index"`
	ToolCallID     string         `json:"tool_call_id"`
	ToolName       string         `json:"tool_name"`
	ToolType       string         `json:"tool_type"`
	ProviderType   string         `json:"provider_type"`
	ProviderID     string         `json:"provider_id"`
	ServerID       string         `json:"server_id"`
	CapabilityID   string         `json:"capability_id"`
	Arguments      map[string]any `json:"arguments"`
	RawResult      any            `json:"raw_result"`
	RawText        string         `json:"raw_text"`
	Summary        string         `json:"summary"`
	Status         string         `json:"status"`
	ErrorMessage   string         `json:"error_message"`
	LatencyMs      int64          `json:"latency_ms"`
	RawTruncated   bool           `json:"raw_truncated"`
	Metadata       map[string]any `json:"metadata"`
	StartedAt      time.Time      `json:"started_at"`
	EndedAt        time.Time      `json:"ended_at"`
	CreatedAt      time.Time      `json:"created_at"`
}

// AgentTraceEvent is an append-only execution trajectory event. Large tool raw
// IO is linked through ToolTraceID instead of duplicated here.
type AgentTraceEvent struct {
	ID               string    `json:"id"`
	TraceID          string    `json:"trace_id"`
	ExecutionID      string    `json:"execution_id"`
	ConversationID   string    `json:"conversation_id"`
	AgentID          string    `json:"agent_id"`
	UserID           string    `json:"user_id"`
	RunType          string    `json:"run_type"`
	ObservationType  string    `json:"observation_type"`
	EventType        string    `json:"event_type"`
	StepIndex        int       `json:"step_index"`
	SpanName         string    `json:"span_name"`
	ParentEventID    string    `json:"parent_event_id"`
	Status           string    `json:"status"`
	Input            any       `json:"input"`
	Output           any       `json:"output"`
	Summary          string    `json:"summary"`
	ErrorMessage     string    `json:"error_message"`
	Model            string    `json:"model"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	CostUSD          float64   `json:"cost_usd"`
	LatencyMs        int64     `json:"latency_ms"`
	ToolTraceID      string    `json:"tool_trace_id"`
	ProviderType     string    `json:"provider_type"`
	ProviderID       string    `json:"provider_id"`
	NodeID           string    `json:"node_id"`
	NodeType         string    `json:"node_type"`
	WorkflowID       string    `json:"workflow_id"`
	WorkflowVersion  string    `json:"workflow_version"`
	SequenceNo       int64     `json:"sequence_no"`
	Metadata         any       `json:"metadata"`
	OTelTraceID      string    `json:"otel_trace_id"`
	OTelSpanID       string    `json:"otel_span_id"`
	StartedAt        time.Time `json:"started_at"`
	EndedAt          time.Time `json:"ended_at"`
	CreatedAt        time.Time `json:"created_at"`
}

// AgentExecutionCheckpoint is the resumable runtime snapshot for a long-running
// agent execution. It is not used as audit history; trace events remain
// append-only history.
type AgentExecutionCheckpoint struct {
	ID                     string          `json:"id"`
	ExecutionID            string          `json:"execution_id"`
	TraceID                string          `json:"trace_id"`
	ConversationID         string          `json:"conversation_id"`
	AgentID                string          `json:"agent_id"`
	UserID                 string          `json:"user_id"`
	CurrentNode            string          `json:"current_node"`
	StepIndex              int             `json:"step_index"`
	MessagesSnapshotJSON   json.RawMessage `json:"messages_snapshot_json"`
	PendingToolCallsJSON   json.RawMessage `json:"pending_tool_calls_json"`
	CompletedToolCallsJSON json.RawMessage `json:"completed_tool_calls_json"`
	RuntimeStateJSON       json.RawMessage `json:"runtime_state_json"`
	Status                 string          `json:"status"`
	ResumeReason           string          `json:"resume_reason"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
	ExpiresAt              time.Time       `json:"expires_at"`
}

// AgentResult holds the output of a completed agent execution.
type AgentResult struct {
	AgentID          string
	Input            string
	Output           string
	Thoughts         []Thought
	ToolCalls        []ToolCall
	ToolObservations []ToolObservation
	TraceEvents      []AgentTraceEvent
	Steps            int
	TokensUsed       int
	CostUSD          float64
	Duration         time.Duration
	Error            error
	Metadata         map[string]interface{}
}

// AgentState tracks mutable execution progress during a single run.
type AgentState struct {
	StepsTaken int
	Thoughts   []Thought
	ToolCalls  []ToolCall
	TokensUsed int
}
