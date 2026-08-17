package application

import (
	"context"
	"strconv"
	"strings"
	"unicode"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

// maybeInjectTaskResume 在新会话执行入口判断是否有可恢复的活跃 task：
// 最新活跃 task 且 next_action 非空且新消息与 task.goal 语义相关 → 注入一条
// system 消息（task 摘要 + continue 指令），让 agent 从 next_action 继续而非
// 从零重述。读失败 fail closed（不注入，防止假装有进度）。调用方保证
// checkpoint plan 未恢复时才进入（executeReAct 仅在 activePlan==nil 时调用）。
func (a *BaseAgent) maybeInjectTaskResume(ctx context.Context, ec agentExecContext, msgs []port.LLMMessage) []port.LLMMessage {
	if a.TaskStore == nil || ec.cfg.UserID == "" {
		return msgs
	}
	taskCtx, cancel := context.WithTimeout(ctx, constants.AgentDBQueryTimeout)
	defer cancel()
	task, err := a.TaskStore.GetLatestActiveForOwner(taskCtx, ec.cfg.TenantID, ec.agentID, ec.cfg.UserID)
	if err != nil {
		a.Logger.Error("agent: task resume load failed, fail closed",
			zap.String("agent_id", ec.agentID), zap.Error(err))
		return msgs
	}
	if task == nil || task.Status != domain.TaskStatusActive || task.NextAction == "" {
		return msgs
	}
	if !semanticallyRelated(task.Goal, ec.input) {
		return msgs
	}
	content := taskResumePrompt(task)
	return append([]port.LLMMessage{{Role: "system", Content: content}}, msgs...)
}

// taskResumePrompt 构造 task 摘要 + continue 指令。fail_count 达到阈值时附加
// "上次多次失败，是否继续"提示（不自动改状态）。
func taskResumePrompt(task *domain.Task) string {
	var b strings.Builder
	b.WriteString("检测到未完成的目标，请基于以下进度继续推进（不要重新规划，执行下一步）：\n")
	b.WriteString("目标：")
	b.WriteString(task.Goal)
	b.WriteString("\n")
	b.WriteString("当前进度：")
	b.WriteString(task.CurrentPhase)
	b.WriteString("\n")
	if len(task.CompletedSteps) > 0 {
		b.WriteString("已完成步骤数：")
		b.WriteString(strconv.Itoa(len(task.CompletedSteps)))
		b.WriteString("\n")
	}
	b.WriteString("下一步：")
	b.WriteString(task.NextAction)
	b.WriteString("\n")
	if task.FailCount >= constants.TaskFailThreshold {
		b.WriteString("注意：该目标上次推进多次失败，请评估是否继续并调整策略。\n")
	}
	return b.String()
}

// semanticallyRelated 判断新消息是否与 task.goal 语义相关：取两者字符 bigram
// 集合，计算 input bigram 对 goal bigram 的覆盖率（命中数/goal 总数）。中文
// 无空格分词，bigram（2 字）粒度对中文与英文均有效，且为纯函数、可测。
func semanticallyRelated(goal, text string) bool {
	if goal == "" || text == "" {
		return false
	}
	goalN := taskNGrams(goal)
	textN := taskNGrams(text)
	if len(goalN) == 0 || len(textN) == 0 {
		return false
	}
	hit := 0
	for ngram := range textN {
		if _, ok := goalN[ngram]; ok {
			hit++
		}
	}
	return float64(hit)/float64(len(goalN)) >= constants.TaskSemanticSimilarityThreshold
}

// taskNGrams 取字符串的 2-gram 集合（小写、去非字母数字字符；短串整体作一个
// token，防止"迁移"与"迁移订单服务"因 bigram 太少而漏匹配）。
func taskNGrams(s string) map[string]struct{} {
	var runes []rune
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			runes = append(runes, r)
		}
	}
	out := make(map[string]struct{})
	if len(runes) < 2 {
		if len(runes) == 1 {
			out[string(runes)] = struct{}{}
		}
		return out
	}
	for i := 0; i+2 <= len(runes); i++ {
		out[string(runes[i:i+2])] = struct{}{}
	}
	return out
}
