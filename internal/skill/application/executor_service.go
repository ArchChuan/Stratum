// Package application provides skill execution orchestration.
package application

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
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
	Get(id string) (domain.Skill, bool)
}

type Executor struct {
	registry SkillRegistry
}

func NewExecutor(registry SkillRegistry) *Executor {
	return &Executor{registry: registry}
}

func (e *Executor) Execute(ctx ExecutionContext) *ExecutionResult {
	start := time.Now()
	result := &ExecutionResult{SkillID: ctx.SkillID, Timestamp: start}

	skill, ok := e.registry.Get(ctx.SkillID)
	if !ok {
		result.Error = fmt.Errorf("skill not found: %s", ctx.SkillID)
		result.Duration = time.Since(start)
		return result
	}

	executor, ok := skill.(domain.SkillExecutor)
	if !ok {
		result.Error = fmt.Errorf("skill is not executable: %s", ctx.SkillID)
		result.Duration = time.Since(start)
		return result
	}

	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	timeout := ctx.Timeout
	if timeout == 0 {
		timeout = domain.DefaultSkillTimeout
	}
	// execCtx is passed to executor so cancellation stops the goroutine
	execCtx, cancel := context.WithTimeout(baseCtx, timeout)
	defer cancel()

	done := make(chan interface{}, 1)
	errChan := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				errChan <- fmt.Errorf("skill panic: %v", r)
			}
		}()
		output, err := executor.Execute(execCtx, ctx.Input)
		if err != nil {
			errChan <- err
		} else {
			done <- output
		}
	}()

	select {
	case output := <-done:
		result.Output = output
	case err := <-errChan:
		result.Error = err
	case <-execCtx.Done():
		result.Error = fmt.Errorf("skill execution timeout: %s", ctx.SkillID)
	}

	result.Duration = time.Since(start)
	return result
}
