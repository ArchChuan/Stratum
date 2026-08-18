package application

import (
	"fmt"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// NoAnswerReason 是 RAG 无答案信号的固定枚举；值定义在 pkg/constants
// （knowledge 与 agent 跨 context 共享的单一事实源，值进入响应契约与指标
// label，禁止动态拼接）。
type NoAnswerReason string

const (
	NoAnswerNoSources            NoAnswerReason = constants.NoAnswerReasonNoSources
	NoAnswerThresholdFiltered    NoAnswerReason = constants.NoAnswerReasonThresholdFiltered
	NoAnswerAccessRestricted     NoAnswerReason = constants.NoAnswerReasonAccessRestricted
	NoAnswerInsufficientEvidence NoAnswerReason = constants.NoAnswerReasonInsufficientEvidence
	NoAnswerUnsupportedMode      NoAnswerReason = constants.NoAnswerReasonUnsupportedMode
)

// NoAnswerInfo 是 RAG 无答案的结构化信号。RAGQueryResult.NoAnswer 为 nil
// = 有答案（Sources 非空）；非 nil = 无答案且 reason 说明原因。BestScore
// 恒有值（无候选时 0），与 NoAnswer 解耦供校准消费。
type NoAnswerInfo struct {
	Reason         NoAnswerReason
	RetrievedCount int     // 阈值过滤前候选数
	FilteredCount  int     // 阈值过滤掉的条数
	BestScore      float32 // 池内最高分（阈值过滤前采集）
	Retried        bool    // 触底自动重试标记（P3 使用）
	RewrittenQuery string  // 重试改写后的 query（P3 使用）
	Detail         string  // 人读摘要，仅固定模板
}

// buildNoAnswer 组装无答案信号。Detail 按 reason 固定模板生成，禁止拼接
// 检索内容等可变数据（信号透出到响应契约与 agent 观察）。
func buildNoAnswer(reason NoAnswerReason, retrieved, filtered int, bestScore float32) *NoAnswerInfo {
	return &NoAnswerInfo{
		Reason:         reason,
		RetrievedCount: retrieved,
		FilteredCount:  filtered,
		BestScore:      bestScore,
		Detail:         noAnswerDetail(reason, retrieved, filtered, bestScore),
	}
}

func noAnswerDetail(reason NoAnswerReason, retrieved, filtered int, bestScore float32) string {
	switch reason {
	case NoAnswerThresholdFiltered:
		return fmt.Sprintf("检索到 %d 条候选，%d 条未达相关性阈值（最高分 %.3f）", retrieved, filtered, bestScore)
	case NoAnswerAccessRestricted:
		return "当前身份在知识库中无可见文档"
	case NoAnswerUnsupportedMode:
		return "当前检索模式不被支持"
	case NoAnswerInsufficientEvidence:
		return "检索到的证据不足以支撑回答"
	default:
		return "知识库中未检索到相关内容"
	}
}

// noAnswerSeverity 是无答案 reason 的严重度排序（聚合时取最严重者）：
// access_restricted（内容存在但不可见，需人工介入）> threshold_filtered /
// insufficient_evidence（有候选被质量门控，校准信号）> no_sources /
// unsupported_mode。
func noAnswerSeverity(r NoAnswerReason) int {
	switch r {
	case NoAnswerAccessRestricted:
		return 3
	case NoAnswerThresholdFiltered, NoAnswerInsufficientEvidence:
		return 2
	default:
		return 1
	}
}

// mergeNoAnswer 按 at-least-one 语义聚合多 workspace 的无答案信号：任一
// workspace 有答案（nil）即整体有答案；全空取严重度最高 reason。
func mergeNoAnswer(acc, cur *NoAnswerInfo) *NoAnswerInfo {
	if cur == nil {
		return nil
	}
	if acc == nil || noAnswerSeverity(cur.Reason) > noAnswerSeverity(acc.Reason) {
		return cur
	}
	return acc
}
