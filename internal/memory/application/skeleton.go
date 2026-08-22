package application

import (
	"encoding/json"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// ToolCallInput 是骨架压缩器的输入：一次工具调用的最小摘要视图。参数摘要
// 由调用方（agent 侧）完成截断/脱敏，骨架压缩器只做聚合与上限控制。
type ToolCallInput struct {
	ToolName    string
	ArgsSummary string
	Status      string
	ErrorMsg    string
	DurationMS  int64
}

// BuildTrajectorySkeleton 把任务结束时的工具调用摘要压缩为反思骨架：
// 确定性规则、无 LLM；丢弃原始参数/返回体；受大小与步数上限约束。
func BuildTrajectorySkeleton(
	executionID, taskGoal, resultSummary, terminatedBy string,
	calls []ToolCallInput,
) (domain.TrajectorySkeleton, error) {
	if executionID == "" {
		return domain.TrajectorySkeleton{}, fmt.Errorf("build trajectory skeleton: execution_id is empty")
	}

	stepMax := constants.MemoryReflectionStepMax
	if len(calls) > stepMax {
		calls = calls[:stepMax]
	}

	steps := make([]domain.TrajectoryStep, 0, len(calls))
	for _, c := range calls {
		status := domain.TrajectoryStepStatusSuccess
		if c.Status == domain.TrajectoryStepStatusError || c.ErrorMsg != "" {
			status = domain.TrajectoryStepStatusError
		}
		steps = append(steps, domain.TrajectoryStep{
			ToolName:         domain.TruncateRunes(c.ToolName, 100),
			ArgsSummary:      domain.TruncateRunes(c.ArgsSummary, constants.MemoryReflectionArgsSummaryMaxRunes),
			Status:           status,
			ErrorFingerprint: domain.TruncateRunes(c.ErrorMsg, constants.MemoryReflectionErrorFingerprintMaxRunes),
			DurationMS:       c.DurationMS,
		})
	}

	skeleton := domain.TrajectorySkeleton{
		ExecutionID:   executionID,
		TaskGoal:      domain.TruncateRunes(taskGoal, 300),
		Steps:         steps,
		ToolStats:     nil,
		ResultSummary: domain.TruncateRunes(resultSummary, constants.MemoryReflectionResultSummaryMaxRunes),
		TerminatedBy:  domain.TruncateRunes(terminatedBy, 100),
	}
	skeleton.ToolStats = skeleton.AggregateToolStats()

	if err := skeleton.Validate(); err != nil {
		return domain.TrajectorySkeleton{}, fmt.Errorf("build trajectory skeleton: %w", err)
	}

	// 大小预算：序列化超限时从尾部丢弃步骤（保留 ≥1 步），防止超长轨迹撑爆
	// 反思 prompt。参数摘要已按 rune 上限截断，正常情况不会触发。
	for len(skeleton.Steps) > 1 {
		raw, err := json.Marshal(skeleton)
		if err != nil {
			return domain.TrajectorySkeleton{}, fmt.Errorf("build trajectory skeleton: marshal: %w", err)
		}
		if len(raw) <= constants.MemoryReflectionSkeletonMaxBytes {
			break
		}
		skeleton.Steps = skeleton.Steps[:len(skeleton.Steps)-1]
		skeleton.ToolStats = skeleton.AggregateToolStats()
	}
	return skeleton, nil
}
