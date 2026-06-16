// Package skill provides skill execution framework.
package skill

import (
	"context"
	"fmt"
	"time"
)

type ExecutionContext struct {
	SkillID   string
	Input     interface{}
	StartTime time.Time
	Timeout   time.Duration
	Ctx       context.Context
}

type ExecutionResult struct {
	SkillID   string
	Output    interface{}
	Error     error
	Duration  time.Duration
	Timestamp time.Time
}

type SkillRegistry interface {
	Get(id string) (Skill, bool)
}

type Executor struct {
	registry SkillRegistry
}

func NewExecutor(registry SkillRegistry) *Executor {
	return &Executor{
		registry: registry,
	}
}

func (e *Executor) Execute(ctx ExecutionContext) *ExecutionResult {
	start := time.Now()
	result := &ExecutionResult{
		SkillID:   ctx.SkillID,
		Timestamp: start,
	}

	skill, ok := e.registry.Get(ctx.SkillID)
	if !ok {
		result.Error = fmt.Errorf("skill not found: %s", ctx.SkillID)
		result.Duration = time.Since(start)
		return result
	}

	executor, ok := skill.(SkillExecutor)
	if !ok {
		result.Error = fmt.Errorf("skill is not executable: %s", ctx.SkillID)
		result.Duration = time.Since(start)
		return result
	}

	done := make(chan interface{}, 1)
	errChan := make(chan error, 1)

	go func() {
		execCtx := ctx.Ctx
		if execCtx == nil {
			execCtx = context.Background()
		}
		output, err := executor.Execute(execCtx, ctx.Input)
		if err != nil {
			errChan <- err
		} else {
			done <- output
		}
	}()

	timeout := ctx.Timeout
	if timeout == 0 {
		timeout = DefaultSkillTimeout
	}

	select {
	case output := <-done:
		result.Output = output
	case err := <-errChan:
		result.Error = err
	case <-time.After(timeout):
		result.Error = fmt.Errorf("skill execution timeout: %s", ctx.SkillID)
	}

	result.Duration = time.Since(start)
	return result
}
