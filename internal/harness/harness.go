// Package harness provides the component harness framework.
package harness

import (
	"context"
	"fmt"
	"sync"
	"time"

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
	h.logger.Info("Component registered", zap.String("component", name))
	return nil
}

// Start initializes all registered components
func (h *Harness) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.started {
		return fmt.Errorf("harness already started")
	}

	h.logger.Info("Starting harness", zap.Int("components", len(h.components)))

	for name, comp := range h.components {
		if err := comp.Start(ctx); err != nil {
			h.logger.Error("Failed to start component", zap.String("component", name), zap.Error(err))
			// Stop any components that were already started
			h.stopStartedComponents(ctx, name)
			return fmt.Errorf("failed to start component %s: %w", name, err)
		}
		h.logger.Info("Component started", zap.String("component", name))
	}

	h.started = true
	h.logger.Info("Harness started successfully")
	return nil
}

// Stop gracefully shuts down all components in reverse order
func (h *Harness) Stop(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.started {
		return nil
	}

	h.logger.Info("Stopping harness")

	// Stop components in reverse registration order
	names := h.getRegistrationOrder()
	for i := len(names) - 1; i >= 0; i-- {
		name := names[i]
		comp := h.components[name]
		if err := comp.Stop(ctx); err != nil {
			h.logger.Error("Failed to stop component", zap.String("component", name), zap.Error(err))
		} else {
			h.logger.Info("Component stopped", zap.String("component", name))
		}
	}

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

// getRegistrationOrder returns component names in registration order
func (h *Harness) getRegistrationOrder() []string {
	order := make([]string, 0, len(h.components))
	for name := range h.components {
		order = append(order, name)
	}
	return order
}

// stopStartedComponents stops all components before the failed one
func (h *Harness) stopStartedComponents(ctx context.Context, failedName string) {
	for name, comp := range h.components {
		if name == failedName {
			break
		}
		if err := comp.Stop(ctx); err != nil {
			h.logger.Error("Failed to stop component during rollback", zap.String("component", name), zap.Error(err))
		}
	}
}

// Run starts the harness and waits for context cancellation
func (h *Harness) Run(ctx context.Context) error {
	// Add startup timeout to prevent hanging during component initialization
	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := h.Start(startCtx); err != nil {
		return err
	}
	defer h.Stop(ctx)

	<-ctx.Done()
	return ctx.Err()
}
