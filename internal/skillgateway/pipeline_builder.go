// Package skillgateway provides skill gateway and routing.
package skillgateway

import "github.com/google/uuid"

// PipelineBuilder 链式 DSL 构建 Pipeline
type PipelineBuilder struct {
	id    string
	steps []*Step
}

// NewPipelineBuilder 创建 builder；id 为空时自动生成
func NewPipelineBuilder(id string) *PipelineBuilder {
	if id == "" {
		id = uuid.New().String()
	}
	return &PipelineBuilder{id: id}
}

// Step 添加顺序步骤
func (b *PipelineBuilder) Step(name, skillID string, input any) *PipelineBuilder {
	b.steps = append(b.steps, &Step{
		Name:    name,
		Type:    StepSequential,
		SkillID: skillID,
		Input:   input,
	})
	return b
}

// ifBuilder 条件分支中间态
type ifBuilder struct {
	parent    *PipelineBuilder
	condition func(StepContext) bool
	name      string
}

// If 开始条件分支，返回中间态
func (b *PipelineBuilder) If(name string, condition func(StepContext) bool) *ifBuilder {
	return &ifBuilder{parent: b, condition: condition, name: name}
}

// thenBuilder 已设置 then 分支的中间态
type thenBuilder struct {
	parent    *PipelineBuilder
	condition func(StepContext) bool
	name      string
	then      *Step
}

// Then 设置条件为真时执行的子 pipeline
func (ib *ifBuilder) Then(sub *PipelineBuilder) *thenBuilder {
	var thenStep *Step
	if len(sub.steps) > 0 {
		thenStep = sub.steps[0]
	}
	return &thenBuilder{
		parent:    ib.parent,
		condition: ib.condition,
		name:      ib.name,
		then:      thenStep,
	}
}

// Else 设置条件为假时执行的子 pipeline，并将条件步骤加入主 pipeline
func (tb *thenBuilder) Else(sub *PipelineBuilder) *PipelineBuilder {
	var elseStep *Step
	if len(sub.steps) > 0 {
		elseStep = sub.steps[0]
	}
	tb.parent.steps = append(tb.parent.steps, &Step{
		Name:      tb.name,
		Type:      StepConditional,
		Condition: tb.condition,
		Then:      tb.then,
		Else:      elseStep,
	})
	return tb.parent
}

// EndIf 不设置 else 分支，直接将条件步骤加入主 pipeline
func (tb *thenBuilder) EndIf() *PipelineBuilder {
	tb.parent.steps = append(tb.parent.steps, &Step{
		Name:      tb.name,
		Type:      StepConditional,
		Condition: tb.condition,
		Then:      tb.then,
	})
	return tb.parent
}

// Parallel 添加并行步骤组
func (b *PipelineBuilder) Parallel(name string, subs ...*PipelineBuilder) *PipelineBuilder {
	var parallelSteps []*Step
	for _, sub := range subs {
		parallelSteps = append(parallelSteps, sub.steps...)
	}
	b.steps = append(b.steps, &Step{
		Name:     name,
		Type:     StepParallel,
		Parallel: parallelSteps,
	})
	return b
}

// Build 构建 Pipeline
func (b *PipelineBuilder) Build() Pipeline {
	return Pipeline{ID: b.id, Steps: b.steps}
}
