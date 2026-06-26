// Package harness provides the component harness framework.
package harness

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"
)

// Component represents a service that can be started and stopped
type Component interface {
	// Name returns the component name
	Name() string

	// Start initializes the component
	Start(ctx context.Context) error

	// Stop gracefully shuts down the component
	Stop(ctx context.Context) error

	// HealthCheck returns the health status of the component
	HealthCheck(ctx context.Context) error
}

// Harness manages the application lifecycle and components
type Harness struct {
	components map[string]Component
	order      []string // registration order, used for deterministic start/stop
	logger     *zap.Logger
	mu         sync.RWMutex
	started    bool
}

// New creates a new Harness instance
func New(logger *zap.Logger) *Harness {
	return &Harness{
		components: make(map[string]Component),
		logger:     logger,
	}
}

// Register adds a component to the harness
func (h *Harness) Register(comp Component) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.started {
		return fmt.Errorf("cannot register component after harness is started")
	}

	name := comp.Name()
	if _, exists := h.components[name]; exists {
		return fmt.Errorf("component already registered: %s", name)
	}

	h.components[name] = comp
	h.order = append(h.order, name)
	h.logger.Info("Component registered", zap.String("component", name))
	return nil
}

// Start initializes all registered components in registration order
func (h *Harness) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.started {
		return fmt.Errorf("harness already started")
	}

	h.logger.Info("Starting harness", zap.Int("components", len(h.components)))

	var started []string
	for _, name := range h.order {
		comp := h.components[name]
		if err := comp.Start(ctx); err != nil {
			h.logger.Error("Failed to start component", zap.String("component", name), zap.Error(err))
			h.stopStartedComponents(ctx, started)
			return fmt.Errorf("failed to start component %s: %w", name, err)
		}
		started = append(started, name)
		h.logger.Info("Component started", zap.String("component", name))
	}

	h.started = true
	h.logger.Info("Harness started successfully")
	return nil
}

// Stop gracefully shuts down all components in reverse registration order
func (h *Harness) Stop(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.started {
		return nil
	}

	h.logger.Info("Stopping harness")
	h.stopStartedComponents(ctx, h.order)
	h.started = false
	h.logger.Info("Harness stopped")
	return nil
}

// HealthCheck performs health checks on all components
func (h *Harness) HealthCheck(ctx context.Context) map[string]error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	results := make(map[string]error)
	for name, comp := range h.components {
		results[name] = comp.HealthCheck(ctx)
	}
	return results
}

// GetComponent retrieves a component by name
func (h *Harness) GetComponent(name string) (Component, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	comp, exists := h.components[name]
	return comp, exists
}

// stopStartedComponents stops the given components in reverse order.
func (h *Harness) stopStartedComponents(ctx context.Context, started []string) {
	for i := len(started) - 1; i >= 0; i-- {
		name := started[i]
		if err := h.components[name].Stop(ctx); err != nil {
			h.logger.Error("Failed to stop component", zap.String("component", name), zap.Error(err))
		} else {
			h.logger.Info("Component stopped", zap.String("component", name))
		}
	}
}

// Run starts the harness and waits for context cancellation
func (h *Harness) Run(ctx context.Context) error {
	if err := h.Start(ctx); err != nil {
		return err
	}
	defer h.Stop(ctx) //nolint:errcheck

	<-ctx.Done()
	return ctx.Err()
}
