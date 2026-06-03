// Package skillgateway provides skill gateway and routing.
package skillgateway

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// StepType pipeline 步骤类型
type StepType string

const (
	StepSequential  StepType = "sequential"
	StepConditional StepType = "conditional"
	StepParallel    StepType = "parallel"
)

// StepContext 步骤间共享上下文，key 格式：$steps.<name>.output
type StepContext map[string]any

// Step pipeline 中的单个步骤
type Step struct {
	Name      string
	Type      StepType
	SkillID   string
	Input     any
	Condition func(StepContext) bool
	Then      *Step
	Else      *Step
	Parallel  []*Step
}

// Pipeline 多步骤编排定义
type Pipeline struct {
	ID    string
	Steps []*Step
}

// StepResult 单步骤执行结果
type StepResult struct {
	Name     string
	Output   any
	Error    error
	Duration time.Duration
}

// PipelineResult pipeline 整体执行结果
type PipelineResult struct {
	PipelineID string
	TraceID    string
	Steps      []StepResult
	Duration   time.Duration
}

// PipelineError pipeline 步骤失败错误
type PipelineError struct {
	*SkillError
	FailedStep string
	StepIndex  int
}

func (e *PipelineError) Error() string {
	return fmt.Sprintf("pipeline step %q (index %d) failed: %s", e.FailedStep, e.StepIndex, e.SkillError.Error())
}

// pipelineEngine 执行 Pipeline
type pipelineEngine struct {
	atomic *atomicEngine
	logger *zap.Logger
}

func newPipelineEngine(ae *atomicEngine, logger *zap.Logger) *pipelineEngine {
	return &pipelineEngine{atomic: ae, logger: logger.Named("pipeline_engine")}
}

func (e *pipelineEngine) execute(ctx context.Context, p Pipeline) (PipelineResult, error) {
	traceID := uuid.New().String()
	ctx = context.WithValue(ctx, traceIDKey, traceID)

	start := time.Now()
	stepCtx := make(StepContext)
	var results []StepResult
	var prevName string

	for i, step := range p.Steps {
		sr, err := e.executeStep(ctx, step, stepCtx, prevName, traceID)
		results = append(results, sr)
		if err != nil {
			var skillErr *SkillError
			if se, ok := err.(*SkillError); ok {
				skillErr = se
			} else {
				skillErr = &SkillError{Code: ErrPipelineStepFailed, Message: err.Error(), TraceID: traceID, Cause: err}
			}
			return PipelineResult{PipelineID: p.ID, TraceID: traceID, Steps: results, Duration: time.Since(start)},
				&PipelineError{SkillError: skillErr, FailedStep: step.Name, StepIndex: i}
		}
		if step.Name != "" {
			stepCtx["$steps."+step.Name+".output"] = sr.Output
			prevName = step.Name
		}
	}

	return PipelineResult{
		PipelineID: p.ID,
		TraceID:    traceID,
		Steps:      results,
		Duration:   time.Since(start),
	}, nil
}

func (e *pipelineEngine) executeStep(ctx context.Context, step *Step, stepCtx StepContext, prevName, traceID string) (StepResult, error) {
	start := time.Now()

	switch step.Type {
	case StepConditional:
		return e.executeConditional(ctx, step, stepCtx, prevName, traceID)
	case StepParallel:
		return e.executeParallel(ctx, step, stepCtx, traceID)
	default: // StepSequential or zero value
		return e.executeAtomic(ctx, step, stepCtx, prevName, traceID, start)
	}
}

func (e *pipelineEngine) executeAtomic(ctx context.Context, step *Step, stepCtx StepContext, prevName, traceID string, start time.Time) (StepResult, error) {
	input := resolveInput(step.Input, stepCtx, prevName)
	resp, err := e.atomic.execute(ctx, SkillRequest{
		TraceID: traceID,
		SkillID: step.SkillID,
		Input:   input,
	})
	if err != nil {
		return StepResult{Name: step.Name, Error: err, Duration: time.Since(start)}, err
	}
	return StepResult{Name: step.Name, Output: resp.Output, Duration: time.Since(start)}, nil
}

func (e *pipelineEngine) executeConditional(ctx context.Context, step *Step, stepCtx StepContext, prevName, traceID string) (StepResult, error) {
	start := time.Now()
	var chosen *Step
	if step.Condition != nil && step.Condition(stepCtx) {
		chosen = step.Then
	} else {
		chosen = step.Else
	}
	if chosen == nil {
		return StepResult{Name: step.Name, Duration: time.Since(start)}, nil
	}
	return e.executeStep(ctx, chosen, stepCtx, prevName, traceID)
}

func (e *pipelineEngine) executeParallel(ctx context.Context, step *Step, stepCtx StepContext, traceID string) (StepResult, error) {
	start := time.Now()
	type result struct {
		sr  StepResult
		err error
	}
	results := make([]result, len(step.Parallel))
	var wg sync.WaitGroup

	for i, ps := range step.Parallel {
		wg.Add(1)
		go func(idx int, s *Step) {
			defer wg.Done()
			sr, err := e.executeStep(ctx, s, stepCtx, "", traceID)
			results[idx] = result{sr: sr, err: err}
		}(i, ps)
	}
	wg.Wait()

	var firstErr error
	outputs := make(map[string]any, len(results))
	for _, r := range results {
		if r.err != nil && firstErr == nil {
			firstErr = r.err
		}
		if r.sr.Name != "" {
			outputs[r.sr.Name] = r.sr.Output
		}
	}
	return StepResult{Name: step.Name, Output: outputs, Duration: time.Since(start)}, firstErr
}

// resolveInput 将 $steps.<name>.output 和 $prev.output 替换为实际值
func resolveInput(input any, stepCtx StepContext, prevName string) any {
	s, ok := input.(string)
	if !ok {
		return input
	}
	if s == "$prev.output" && prevName != "" {
		s = "$steps." + prevName + ".output"
	}
	if strings.HasPrefix(s, "$steps.") {
		if v, ok := stepCtx[s]; ok {
			return v
		}
		return ""
	}
	return input
}
