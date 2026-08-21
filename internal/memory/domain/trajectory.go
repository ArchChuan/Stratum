package domain

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// TrajectoryStepStatus 是轨迹骨架中单步工具调用的状态枚举。
const (
	TrajectoryStepStatusSuccess = "success"
	TrajectoryStepStatusError   = "error"
)

// TrajectoryStep 是单次工具调用的压缩骨架：名称、参数摘要、状态、错误指纹、
// 耗时。不携带原始参数值或工具返回体（原始 tool steps 不直接入库）。
type TrajectoryStep struct {
	ToolName         string `json:"tool_name"`
	ArgsSummary      string `json:"args_summary,omitempty"`
	Status           string `json:"status"`
	ErrorFingerprint string `json:"error_fingerprint,omitempty"`
	DurationMS       int64  `json:"duration_ms,omitempty"`
}

// ToolStat 是按工具聚合的调用统计。
type ToolStat struct {
	Count      int `json:"count"`
	ErrorCount int `json:"error_count"`
	RetryCount int `json:"retry_count"`
}

// TrajectorySkeleton 是任务结束时的确定性压缩轨迹，作为反思模型的输入。
// 它本身不是记忆条目，只存在于任务 payload 中，不持久化。
type TrajectorySkeleton struct {
	ExecutionID   string              `json:"execution_id"`
	TaskGoal      string              `json:"task_goal,omitempty"`
	Steps         []TrajectoryStep    `json:"steps"`
	ToolStats     map[string]ToolStat `json:"tool_stats"`
	ResultSummary string              `json:"result_summary,omitempty"`
	TerminatedBy  string              `json:"terminated_by,omitempty"`
}

// Validate 校验骨架结构完整性：execution_id 非空、至少一个步骤、步骤字段
// 合法。长度约束由 SkeletonBuilder 压缩时保证，此处只做结构校验。
func (s *TrajectorySkeleton) Validate() error {
	if s == nil {
		return fmt.Errorf("trajectory skeleton is nil")
	}
	if strings.TrimSpace(s.ExecutionID) == "" {
		return fmt.Errorf("trajectory skeleton: execution_id is empty")
	}
	if len(s.Steps) == 0 {
		return fmt.Errorf("trajectory skeleton: no steps")
	}
	for i, step := range s.Steps {
		if strings.TrimSpace(step.ToolName) == "" {
			return fmt.Errorf("trajectory skeleton: step %d tool_name is empty", i)
		}
		if step.Status != TrajectoryStepStatusSuccess && step.Status != TrajectoryStepStatusError {
			return fmt.Errorf("trajectory skeleton: step %d invalid status %q", i, step.Status)
		}
	}
	return nil
}

// ShouldReflect 是反思触发 gate（确定性规则，非 LLM）：
// 显式记忆指令或工具调用数达标或存在失败/重试时触发；单次只读查询等
// 低价值任务（工具少、无失败、无指令）不触发。
func (s *TrajectorySkeleton) ShouldReflect(minToolCalls int, explicitMemory bool) bool {
	if s == nil || len(s.Steps) == 0 {
		return false
	}
	if explicitMemory {
		return true
	}
	if len(s.Steps) >= minToolCalls {
		return true
	}
	for _, step := range s.Steps {
		if step.Status == TrajectoryStepStatusError {
			return true
		}
	}
	return false
}

// AggregateToolStats 聚合步骤为按工具统计（确定性规则，供反思模型参考）。
func (s *TrajectorySkeleton) AggregateToolStats() map[string]ToolStat {
	stats := make(map[string]ToolStat, len(s.Steps))
	for _, step := range s.Steps {
		st := stats[step.ToolName]
		st.Count++
		if step.Status == TrajectoryStepStatusError {
			st.ErrorCount++
		}
		stats[step.ToolName] = st
	}
	return stats
}

// TruncateRunes 按 rune 数截断字符串；超长时追加省略标记。
func TruncateRunes(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes])
}

// TruncateRunesLen 返回 rune 长度（供骨架压缩大小预算计算）。
func TruncateRunesLen(value string) int {
	return utf8.RuneCountInString(value)
}
