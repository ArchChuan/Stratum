package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

// judgeSufficiencyGate 是生成前证据充分性门（仅 evidence 路径挂载，由
// searchWorkspaceWithEvidence 调用）：相似度阈值回答"像不像"，judge 回答
// "能不能推出结论"。判 INSUFFICIENT 时该 workspace 按无内容处理（Sources
// 置空 + NoAnswer=insufficient_evidence，维持 content=="" ⇒ NoAnswer!=nil
// 不变量），经聚合严重度排序上报。fail-closed：model 为空（judge 门关闭）/
// resolver 未装配/解析失败/调用失败/超时 → 原样放行（WARN 留痕），行为与不
// 配置时完全一致，绝不误杀检索。
func (rs *RAGService) judgeSufficiencyGate(ctx context.Context, tenantID, workspace, query, model, instructions string, result *RAGQueryResult) *RAGQueryResult {
	if model == "" || rs.judgeResolver == nil || len(result.Sources) == 0 {
		return result
	}
	judge, err := rs.judgeResolver(ctx, model)
	if err != nil {
		rs.logger.Warn("knowledge.judge.sufficiency_degraded",
			zap.String("tenant_id", tenantID), zap.String("workspace", workspace),
			zap.String("model", model), zap.Error(err))
		return result
	}
	if judge == nil {
		rs.logger.Warn("knowledge.judge.sufficiency_unavailable",
			zap.String("tenant_id", tenantID), zap.String("workspace", workspace),
			zap.String("model", model))
		return result
	}
	verdict, err := judge.JudgeSufficiency(ctx, query, formatSources(result.Sources), instructions)
	if err != nil {
		rs.logger.Warn("knowledge.judge.sufficiency_degraded",
			zap.String("tenant_id", tenantID), zap.String("workspace", workspace),
			zap.String("model", model), zap.Error(err))
		return result
	}
	if verdict != port.SufficiencyInsufficient {
		return result
	}
	if rs.metrics != nil {
		rs.metrics.IncNoAnswer(tenantID, constants.NoAnswerReasonInsufficientEvidence)
	}
	return &RAGQueryResult{
		NoAnswer:       buildNoAnswer(NoAnswerInsufficientEvidence, result.CandidateCount, 0, result.BestScore),
		BestScore:      result.BestScore,
		CandidateCount: result.CandidateCount,
	}
}
