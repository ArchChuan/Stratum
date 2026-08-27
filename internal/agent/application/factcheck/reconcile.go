package factcheck

import (
	"regexp"
	"strings"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// 对账分类常量（五态），值与前端 ToolReferenceClassification union 一一对应。
// verified 不失效；verification_failed / invalid_reference 使整体 IsValid=false；
// outcome_unknown 与 unverified 是 advisory（只升 Risk，不硬判假话）。
const (
	ClassVerified           = "verified"
	ClassVerificationFailed = "verification_failed"
	ClassOutcomeUnknown     = "outcome_unknown"
	ClassInvalidReference   = "invalid_reference"
	ClassUnverified         = "unverified"
)

// toolRefRe 匹配 <tool_ref:ID> 与兼容形式 [tool:ID]；ID 取真实 tool_call_id 的
// 合法字符集（字母/数字/_/-）。v1 只保证 <tool_ref:ID> 形式，[tool:ID] 兼容旧。
var toolRefRe = regexp.MustCompile(`<tool_ref:([A-Za-z0-9_\-]+)>|\[tool:([A-Za-z0-9_\-]+)\]`)

// accomplishmentMarkers 是副作用声称白名单（保守，只匹配确凿执行声称）。命中
// 且未带引用 → unverified 软标记；不在列表内的中性/未完成陈述不误判。
var accomplishmentMarkers = []string{
	"已执行", "已完成", "已创建", "已删除", "已更新", "已修改",
	"已发送", "已启用", "已禁用", "成功调用", "成功执行",
	"executed", "completed", "created", "deleted", "updated",
	"sent", "enabled", "disabled",
}

// reconcileCitations 对账 agent 最终输出中的工具引用与本次执行的内存
// ToolObservation 记录，产出逐引用判定 + 未验证声称。这是"声称必须可追溯"的
// 代码级强制：引用命中成功记录 → verified；命中失败/未知记录 → 显式标注；
// 引用无效（无对应记录）→ invalid_reference；无引用但命中副作用声称 →
// unverified（advisory，不硬判假话）。
func reconcileCitations(output string, observations []domain.ToolObservation) ([]domain.ToolReferenceVerdict, []string) {
	byID := indexByToolCallID(observations)
	var references []domain.ToolReferenceVerdict
	var unverified []string
	seen := make(map[string]bool)
	for _, sentence := range extractClaims(output, 0) {
		matches := toolRefRe.FindAllStringSubmatch(sentence, -1)
		if len(matches) == 0 {
			if isUnverifiedClaim(sentence, len(unverified)) {
				unverified = append(unverified, sentence)
			}
			continue
		}
		references = appendReferencesFor(sentence, matches, byID, seen, references)
	}
	return references, unverified
}

// indexByToolCallID 按 ToolCallID 索引观察记录（唯一键，天然去重）。
func indexByToolCallID(observations []domain.ToolObservation) map[string]domain.ToolObservation {
	byID := make(map[string]domain.ToolObservation, len(observations))
	for _, obs := range observations {
		if obs.ToolCallID != "" {
			byID[obs.ToolCallID] = obs
		}
	}
	return byID
}

// appendReferencesFor 收集一条句子内的全部引用判定，同 ID 去重（seen 为 map，
// 引用类型，调用方可见）。返回追加后的切片（slice 按值传递需回写）。
func appendReferencesFor(sentence string, matches [][]string, byID map[string]domain.ToolObservation, seen map[string]bool, references []domain.ToolReferenceVerdict) []domain.ToolReferenceVerdict {
	for _, m := range matches {
		id := firstNonEmpty(m[1], m[2])
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		references = append(references, classifyReference(sentence, id, m[0], byID))
	}
	return references
}

// isUnverifiedClaim 无引用句子的未验证软标记判定：命中副作用白名单且在计数上限内。
func isUnverifiedClaim(sentence string, unverifiedCount int) bool {
	return unverifiedCount < constants.AgentFactCheckMaxUnverified && isAccomplishment(sentence)
}

// classifyReference 把单个引用与观察记录对账，产出五态判定。引用无对应观察
// 记录 = 声称无效（invalid_reference）；记录为成功 = verified；记录为确凿失败
// 或从未发出 = verification_failed；其余（结果未知 / legacy 无 outcome）= 保守
// outcome_unknown。
func classifyReference(claimText, id, ref string, byID map[string]domain.ToolObservation) domain.ToolReferenceVerdict {
	obs, ok := byID[id]
	if !ok {
		return domain.ToolReferenceVerdict{
			ClaimText: claimText, Reference: ref, ToolCallID: id,
			Classification: ClassInvalidReference, Risk: 4,
		}
	}
	verdict := domain.ToolReferenceVerdict{
		ClaimText: claimText, Reference: ref, ToolCallID: id,
		ToolName: obs.ToolName, Status: obs.Status, Outcome: obs.Outcome,
	}
	switch {
	case obs.Status == domain.ToolTraceStatusSuccess:
		verdict.Classification = ClassVerified
	case obs.Outcome == string(port.ToolExecutionOutcomeDefiniteFailure) ||
		obs.Outcome == string(port.ToolExecutionOutcomeNotSent):
		verdict.Classification = ClassVerificationFailed
		verdict.Risk = 5
	default:
		verdict.Classification = ClassOutcomeUnknown
		verdict.Risk = 2
	}
	return verdict
}

// isAccomplishment 判定句子是否含确凿的副作用执行声称（accomplishment 白名单）。
func isAccomplishment(s string) bool {
	lower := strings.ToLower(s)
	for _, marker := range accomplishmentMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// firstNonEmpty 返回第一个非空字符串（正则两个捕获组中取命中者）。
func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
