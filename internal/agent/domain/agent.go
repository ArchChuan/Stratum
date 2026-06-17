// Package domain holds canonical agent context types and sentinels.
//
// This file is the single source of truth for agent / chat / execution
// data shapes shared across application + infrastructure layers.
// Application keeps thin type aliases (`type X = domain.X`) so existing
// call-sites remain source-compatible after the layering refactor.

package domain

import (
	"encoding/json"
	"errors"
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
	Persona                        string
	SystemPrompt                   string
	LLMModel                       string
	EmbedModel                     string
	MaxIterations                  int
	AllowedSkills                  []string
	MCPServerIDs                   []string
	Capabilities                   []AgentCapability
	KnowledgeWorkspaceIDs          []string
	KnowledgeWorkspaceNames        []string
	KnowledgeWorkspaceDescriptions []string
	MaxContextTokens               int
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
	Role           string // "user" | "agent"
	Content        string
	StepsJSON      json.RawMessage
	IsError        bool
	CreatedAt      time.Time
	UserID         string
	AgentID        string
}

// ExecutionRecord is an agent execution history entry.
type ExecutionRecord struct {
	ID            string
	AgentID       string
	AgentName     string
	UserID        string
	Status        string
	InputPreview  string
	OutputPreview string
	ErrorMessage  string
	TotalTokens   int
	DurationMs    int
	CreatedAt     time.Time
}

// ListOptions controls pagination for execution history queries.
type ListOptions struct {
	Page     int
	PageSize int
}

// Sentinel errors returned by repositories. Application layer aliases
// these (`var ErrNotFound = domain.ErrNotFound`) so external call-sites
// keep matching with `errors.Is`.
var (
	// ErrNotFound is returned when an agent / conversation / message
	// cannot be located in the tenant schema.
	ErrNotFound = errors.New("agent not found")

	// ErrNameConflict is returned when an agent with the same name
	// already exists in the tenant.
	ErrNameConflict = errors.New("agent name already exists")

	// ErrInvalidSkill is returned when a skill ID does not exist in
	// the tenant's skills table.
	ErrInvalidSkill = errors.New("skill not found")
)
