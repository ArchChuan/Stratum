package agent

import (
	"fmt"
	"sync"

	"go.uber.org/zap"
)

// Registry manages the collection of agents
type Registry struct {
	agents   map[string]Agent
	logger   *zap.Logger
	mu       sync.RWMutex
}

// NewRegistry creates a new agent registry
func NewRegistry(logger *zap.Logger) *Registry {
	return &Registry{
		agents: make(map[string]Agent),
		logger: logger,
		mu:     sync.RWMutex{},
	}
}

// Register adds a new agent to the registry
func (r *Registry) Register(agent Agent) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	config := agent.GetConfig()
	if _, exists := r.agents[config.ID]; exists {
		return fmt.Errorf("agent with ID %s already registered", config.ID)
	}

	r.agents[config.ID] = agent

	r.logger.Info("agent registered",
		zap.String("agent_id", config.ID),
		zap.String("name", config.Name),
		zap.String("type", string(config.Type)))

	return nil
}

// Get retrieves an agent by ID
func (r *Registry) Get(id string) (Agent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, exists := r.agents[id]
	return agent, exists
}

// GetAll returns all registered agents
func (r *Registry) GetAll() []Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agents := make([]Agent, 0, len(r.agents))
	for _, agent := range r.agents {
		agents = append(agents, agent)
	}

	return agents
}

// Remove removes an agent from the registry
func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.agents[id]; !exists {
		r.logger.Warn("attempted to remove non-existent agent",
			zap.String("agent_id", id))
		return fmt.Errorf("agent with ID %s not found", id)
	}

	delete(r.agents, id)

	r.logger.Info("agent removed",
		zap.String("agent_id", id))

	return nil
}
