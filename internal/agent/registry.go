package agent

import (
	"sync"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/orchestrator"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"go.uber.org/zap"
)

// Registry manages the collection of agents
type Registry struct {
	agents     map[string]*Agent
	orchestrator *orchestrator.Registry
	gateway    *llmgateway.Gateway
	logger     *zap.Logger
	mu         sync.RWMutex
}

// NewRegistry creates a new agent registry
func NewRegistry(orchestrator *orchestrator.Registry, gateway *llmgateway.Gateway, logger *zap.Logger) *Registry {
	return &Registry{
		agents:       make(map[string]*Agent),
		orchestrator: orchestrator,
		gateway:      gateway,
		logger:       logger,
	}
}

// Register adds a new agent to the registry
func (r *Registry) Register(agent *Agent) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Assign dependencies
	agent.SetOrchestrator(r.orchestrator)
	agent.SetGateway(r.gateway)
	agent.SetLogger(r.logger)

	r.agents[agent.ID] = agent
}

// Get retrieves an agent by ID
func (r *Registry) Get(id string) (*Agent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, exists := r.agents[id]
	return agent, exists
}

// GetAll returns all agents
func (r *Registry) GetAll() []*Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agents := make([]*Agent, 0, len(r.agents))
	for _, agent := range r.agents {
		agents = append(agents, agent)
	}
	return agents
}

// Remove removes an agent from the registry
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.agents, id)
}