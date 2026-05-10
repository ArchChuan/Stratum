package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/orchestrator"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/skill"
	"go.uber.org/zap"
)

// Agent represents an AI agent that can execute tasks using skills
type Agent struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	Description   string                 `json:"description"`
	Persona       string                 `json:"persona"`
	AllowedSkills []string               `json:"allowed_skills"`
	SystemPrompt  string                 `json:"system_prompt"`
	LLMModel      string                 `json:"llm_model"`
	MaxIterations int                    `json:"max_iterations"`
	LLMGateway    *llmgateway.Gateway    `json:"-"`
	Logger        *zap.Logger            `json:"-"`
	orchestrator  *orchestrator.Registry `json:"-"`
	mu            sync.Mutex             `json:"-"`
}

// AgentTask represents a task that an agent will execute
type AgentTask struct {
	Query     string                 `json:"query"`
	Context   map[string]interface{} `json:"context"`
	Variables map[string]interface{} `json:"variables"`
}

// AgentResponse represents the response from an agent
type AgentResponse struct {
	TaskID string                 `json:"task_id"`
	Status string                 `json:"status"` // "running", "completed", "failed"
	Result interface{}            `json:"result,omitempty"`
	Steps  []AgentStep            `json:"steps,omitempty"`
	Error  string                 `json:"error,omitempty"`
	Usage  map[string]interface{} `json:"usage,omitempty"`
}

// AgentStep represents a single step in agent execution
type AgentStep struct {
	Iteration int         `json:"iteration"`
	Action    string      `json:"action"` // "planning", "skill_execution", "reasoning", "finalizing"
	Tool      string      `json:"tool,omitempty"`
	Input     interface{} `json:"input,omitempty"`
	Output    interface{} `json:"output,omitempty"`
}

// NewAgent creates a new agent instance
func NewAgent(id, name, description, persona, systemPrompt, llmModel string, maxIterations int, allowedSkills []string) *Agent {
	return &Agent{
		ID:            id,
		Name:          name,
		Description:   description,
		Persona:       persona,
		SystemPrompt:  systemPrompt,
		LLMModel:      llmModel,
		MaxIterations: maxIterations,
		AllowedSkills: allowedSkills,
	}
}

// SetOrchestrator assigns the orchestrator to the agent
func (a *Agent) SetOrchestrator(o *orchestrator.Registry) {
	a.orchestrator = o
}

// SetGateway assigns the LLM gateway to the agent
func (a *Agent) SetGateway(g *llmgateway.Gateway) {
	a.LLMGateway = g
}

// SetLogger assigns the logger to the agent
func (a *Agent) SetLogger(l *zap.Logger) {
	a.Logger = l
}

// Execute runs the agent with the given task
func (a *Agent) Execute(ctx context.Context, task *AgentTask) (*AgentResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	response := &AgentResponse{
		TaskID: "task_" + a.ID,
		Status: "running",
		Steps:  []AgentStep{},
	}

	// Initial planning step
	planningStep := AgentStep{
		Iteration: 0,
		Action:    "planning",
		Input:     task.Query,
	}

	// Plan the execution based on the query
	plan, err := a.planExecution(task)
	if err != nil {
		response.Status = "failed"
		response.Error = fmt.Sprintf("Planning failed: %v", err)
		return response, err
	}

	planningStep.Output = plan
	response.Steps = append(response.Steps, planningStep)

	// Execute the plan
	currentContext := task.Context
	if currentContext == nil {
		currentContext = make(map[string]interface{})
	}

	for i := 0; i < a.MaxIterations; i++ {
		// Check if context contains a final result
		if finalResult, exists := currentContext["final_result"]; exists {
			response.Status = "completed"
			response.Result = finalResult
			return response, nil
		}

		// Determine next action using LLM
		action, err := a.determineAction(task, currentContext)
		if err != nil {
			response.Status = "failed"
			response.Error = fmt.Sprintf("Action determination failed: %v", err)
			return response, err
		}

		step := AgentStep{
			Iteration: i + 1,
			Action:    "reasoning",
			Input:     action,
		}

		// If action is to execute a skill
		if action.Type == "skill_execution" {
			step.Action = "skill_execution"
			step.Tool = action.Data["skill_id"].(string)

			skillOutput, err := a.executeSkill(action.Data["skill_id"].(string), action.Data["input"])
			if err != nil {
				response.Status = "failed"
				response.Error = fmt.Sprintf("Skill execution failed: %v", err)
				return response, err
			}

			step.Input = action.Data
			step.Output = skillOutput
			currentContext["last_skill_result"] = skillOutput
		} else if action.Type == "final_response" {
			response.Status = "completed"
			response.Result = action.Data["response"]
			currentContext["final_result"] = action.Data["response"]
			step.Output = action.Data["response"]
		}

		response.Steps = append(response.Steps, step)
	}

	// Max iterations reached
	response.Status = "completed"
	response.Result = currentContext
	return response, nil
}

// planExecution creates an initial plan based on the query
func (a *Agent) planExecution(task *AgentTask) (interface{}, error) {
	// In a real implementation, this would involve calling the LLM to create an execution plan
	// For now, we'll return a simple placeholder
	return map[string]interface{}{
		"query":       task.Query,
		"approach":    "Direct execution with available skills",
		"skills_used": a.AllowedSkills,
	}, nil
}

// determineAction uses the LLM to determine the next action
func (a *Agent) determineAction(task *AgentTask, currentContext map[string]interface{}) (*Action, error) {
	if a.LLMGateway == nil {
		return nil, fmt.Errorf("LLM gateway not configured")
	}

	// Prepare the prompt for the LLM
	contextJSON, _ := json.Marshal(currentContext)
	taskJSON, _ := json.Marshal(task)

	prompt := fmt.Sprintf(`
%s

Current context: %s
Task: %s

Based on the persona and current context, determine the next action. You can either:
1. Execute a skill from your allowed skills: %v
2. Generate a direct response to the user

Respond in JSON format with the following structure:
{
  "type": "skill_execution" or "final_response",
  "data": {
    // If skill_execution:
    "skill_id": "skill_id",
    "input": {...}
    // If final_response:
    "response": "your response"
  }
}
If the task can be completed without using any skills, choose "final_response".
`, a.SystemPrompt, string(contextJSON), string(taskJSON), a.AllowedSkills)

	req := &llmgateway.CompletionRequest{
		Model: a.LLMModel,
		Messages: []llmgateway.Message{
			{
				Role:    "user",
				Content: prompt,
			},
		},
		Temperature: 0.1, // Lower temperature for more consistent behavior
	}

	resp, err := a.LLMGateway.Complete(context.Background(), req)
	if err != nil {
		return nil, err
	}

	// Parse the response
	var action Action
	err = json.Unmarshal([]byte(resp.Content), &action)
	if err != nil {
		return nil, fmt.Errorf("failed to parse LLM response as action: %v", err)
	}

	return &action, nil
}

// executeSkill executes a skill with the given input
func (a *Agent) executeSkill(skillID string, input interface{}) (interface{}, error) {
	skillInstance, exists := a.orchestrator.Get(skillID)
	if !exists {
		return nil, fmt.Errorf("skill with ID %s does not exist", skillID)
	}

	// Check if the skill is allowed for this agent
	allowed := false
	for _, allowedSkill := range a.AllowedSkills {
		if allowedSkill == skillID {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("skill %s is not allowed for agent %s", skillID, a.Name)
	}

	executor, ok := skillInstance.(skill.SkillExecutor)
	if !ok {
		return nil, fmt.Errorf("skill %s is not executable", skillID)
	}

	result, err := executor.Execute(input)
	if err != nil {
		return nil, fmt.Errorf("skill execution failed: %v", err)
	}

	return result, nil
}

// Action represents an action determined by the LLM
type Action struct {
	Type string                 `json:"type"` // "skill_execution" or "final_response"
	Data map[string]interface{} `json:"data"`
}
