// Package harness provides the component harness framework.
package harness

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"
)

// BaseComponent provides a base implementation for components
type BaseComponent struct {
	name   string
	logger *zap.Logger
}

// NewBaseComponent creates a new base component
func NewBaseComponent(name string, logger *zap.Logger) *BaseComponent {
	return &BaseComponent{
		name:   name,
		logger: logger,
	}
}

// Name returns the component name
func (b *BaseComponent) Name() string {
	return b.name
}

// Start is a no-op implementation
func (b *BaseComponent) Start(ctx context.Context) error {
	return nil
}

// Stop is a no-op implementation
func (b *BaseComponent) Stop(ctx context.Context) error {
	return nil
}

// HealthCheck returns nil (healthy) by default
func (b *BaseComponent) HealthCheck(ctx context.Context) error {
	return nil
}

// SimpleComponent is a wrapper for simple functions
type SimpleComponent struct {
	*BaseComponent
	startFunc       func(context.Context) error
	stopFunc        func(context.Context) error
	healthCheckFunc func(context.Context) error
}

// SimpleComponentOption configures a SimpleComponent
type SimpleComponentOption func(*SimpleComponent)

// WithStartFunc sets the start function
func WithStartFunc(fn func(context.Context) error) SimpleComponentOption {
	return func(c *SimpleComponent) {
		c.startFunc = fn
	}
}

// WithStopFunc sets the stop function
func WithStopFunc(fn func(context.Context) error) SimpleComponentOption {
	return func(c *SimpleComponent) {
		c.stopFunc = fn
	}
}

// WithHealthCheckFunc sets the health check function
func WithHealthCheckFunc(fn func(context.Context) error) SimpleComponentOption {
	return func(c *SimpleComponent) {
		c.healthCheckFunc = fn
	}
}

// NewSimpleComponent creates a component from functions
func NewSimpleComponent(name string, logger *zap.Logger, opts ...SimpleComponentOption) *SimpleComponent {
	base := NewBaseComponent(name, logger)
	comp := &SimpleComponent{
		BaseComponent: base,
	}

	for _, opt := range opts {
		opt(comp)
	}

	return comp
}

// Start calls the configured start function
func (s *SimpleComponent) Start(ctx context.Context) error {
	if s.startFunc != nil {
		return s.startFunc(ctx)
	}
	return nil
}

// Stop calls the configured stop function
func (s *SimpleComponent) Stop(ctx context.Context) error {
	if s.stopFunc != nil {
		return s.stopFunc(ctx)
	}
	return nil
}

// HealthCheck calls the configured health check function
func (s *SimpleComponent) HealthCheck(ctx context.Context) error {
	if s.healthCheckFunc != nil {
		return s.healthCheckFunc(ctx)
	}
	return nil
}

// ComponentBuilder helps construct components with dependencies
type ComponentBuilder struct {
	name     string
	logger   *zap.Logger
	deps     map[string]interface{}
	startFn  func(context.Context, map[string]interface{}) error
	stopFn   func(context.Context) error
	healthFn func(context.Context) error
}

// NewComponentBuilder creates a new component builder
func NewComponentBuilder(name string, logger *zap.Logger) *ComponentBuilder {
	return &ComponentBuilder{
		name:   name,
		logger: logger,
		deps:   make(map[string]interface{}),
	}
}

// WithDependency adds a dependency
func (b *ComponentBuilder) WithDependency(name string, dep interface{}) *ComponentBuilder {
	b.deps[name] = dep
	return b
}

// WithStart sets the start function with access to dependencies
func (b *ComponentBuilder) WithStart(fn func(context.Context, map[string]interface{}) error) *ComponentBuilder {
	b.startFn = fn
	return b
}

// WithStop sets the stop function
func (b *ComponentBuilder) WithStop(fn func(context.Context) error) *ComponentBuilder {
	b.stopFn = fn
	return b
}

// WithHealthCheck sets the health check function
func (b *ComponentBuilder) WithHealthCheck(fn func(context.Context) error) *ComponentBuilder {
	b.healthFn = fn
	return b
}

// Build creates the component
func (b *ComponentBuilder) Build() Component {
	return &SimpleComponent{
		BaseComponent: NewBaseComponent(b.name, b.logger),
		startFunc: func(ctx context.Context) error {
			if b.startFn != nil {
				return b.startFn(ctx, b.deps)
			}
			return nil
		},
		stopFunc: func(ctx context.Context) error {
			if b.stopFn != nil {
				return b.stopFn(ctx)
			}
			return nil
		},
		healthCheckFunc: func(ctx context.Context) error {
			if b.healthFn != nil {
				return b.healthFn(ctx)
			}
			return nil
		},
	}
}

// DependencyContainer holds resolved dependencies
type DependencyContainer struct {
	deps map[string]interface{}
	mu   sync.RWMutex
}

// NewDependencyContainer creates a new dependency container
func NewDependencyContainer() *DependencyContainer {
	return &DependencyContainer{
		deps: make(map[string]interface{}),
	}
}

// Register registers a dependency
func (c *DependencyContainer) Register(name string, dep interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deps[name] = dep
}

// Get retrieves a dependency
func (c *DependencyContainer) Get(name string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	dep, exists := c.deps[name]
	return dep, exists
}

// MustGet retrieves a dependency or panics
func (c *DependencyContainer) MustGet(name string) interface{} {
	dep, exists := c.Get(name)
	if !exists {
		panic(fmt.Sprintf("dependency not found: %s", name))
	}
	return dep
}

// GetTyped retrieves a typed dependency
func GetTyped[T any](c *DependencyContainer, name string) (T, bool) {
	dep, exists := c.Get(name)
	if !exists {
		var zero T
		return zero, false
	}
	typed, ok := dep.(T)
	return typed, ok
}

// MustGetTyped retrieves a typed dependency or panics
func MustGetTyped[T any](c *DependencyContainer, name string) T {
	dep, ok := GetTyped[T](c, name)
	if !ok {
		panic(fmt.Sprintf("typed dependency not found: %s", name))
	}
	return dep
}
