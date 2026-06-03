// Package model defines API data models and types.

package model

// CreateAgentRequest represents the request to create a new agent
type CreateAgentRequest struct {
	Name          string   `json:"name" binding:"required"`
	Description   string   `json:"description"`
	Persona       string   `json:"persona"`
	SystemPrompt  string   `json:"system_prompt"`
	LLMModel      string   `json:"llm_model" binding:"required"`
	MaxIterations int      `json:"max_iterations" binding:"required,min=1,max=20"`
	AllowedSkills []string `json:"allowed_skills"`
}

// AgentResponse represents the response for agent operations
type AgentResponse struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Persona       string   `json:"persona"`
	SystemPrompt  string   `json:"system_prompt"`
	LLMModel      string   `json:"llm_model"`
	MaxIterations int      `json:"max_iterations"`
	AllowedSkills []string `json:"allowed_skills"`
	CreatedAt     string   `json:"created_at"`
}

// ExecuteAgentRequest represents the request to execute an agent
type ExecuteAgentRequest struct {
	Query     string                 `json:"query" binding:"required"`
	Context   map[string]interface{} `json:"context"`
	Variables map[string]interface{} `json:"variables"`
}

// ExecuteAgentResponse represents the response from executing an agent
type ExecuteAgentResponse struct {
	Result interface{} `json:"result,omitempty"`
	Steps  []AgentStep `json:"steps,omitempty"`
	Status string      `json:"status"`
	Error  string      `json:"error,omitempty"`
}

// AgentStep represents a single step in agent execution
type AgentStep struct {
	Iteration int         `json:"iteration"`
	Action    string      `json:"action"`
	Tool      string      `json:"tool,omitempty"`
	Input     interface{} `json:"input,omitempty"`
	Output    interface{} `json:"output,omitempty"`
}
