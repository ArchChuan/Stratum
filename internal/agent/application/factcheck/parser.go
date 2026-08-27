// Package factcheck 校验 agent 输出的 claim 是否有 RAG 证据支撑（幻觉校验，
// advisory：只生成展示型报告）。编排逻辑在此，judge 由组合根用 llmgateway
// completer 实现——factcheck 不 import llmgateway domain（DDD：跨 context
// 接口定义在消费方）。
package factcheck

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

// ParseClaimVerdicts 解析 judge 返回的 claim 判定 JSON，容错剥 code fence
// （```json / ```）并容忍前后空白；解析失败返回 error（调用方降级为不校验）。
// 该逻辑参考 api/wiring/evaluation.go parseJudgeResponse 复制而来（小接口优先，
// internal/agent/application 不得 import api/wiring）。
func ParseClaimVerdicts(content string) ([]domain.ClaimVerdict, error) {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```json")
		trimmed = strings.TrimPrefix(trimmed, "```")
		trimmed = strings.TrimSuffix(strings.TrimSpace(trimmed), "```")
	}
	var resp struct {
		Claims []domain.ClaimVerdict `json:"claims"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(trimmed)), &resp); err != nil {
		return nil, fmt.Errorf("factcheck: parse judge response: %w", err)
	}
	if resp.Claims == nil {
		return []domain.ClaimVerdict{}, nil
	}
	return normalizeVerdicts(resp.Claims), nil
}

// normalizeVerdicts 归一条目判定：未知 verdict 回退 UNSUPPORTED（judge 输出
// 可能含模型幻觉的枚举值，保守按不可靠处理，使 summarize 据此推导 IsValid=false），
// risk 钳制到 [0,5]（越界值会扭曲报告风险推导）。合法枚举原样保留。
func normalizeVerdicts(verdicts []domain.ClaimVerdict) []domain.ClaimVerdict {
	out := make([]domain.ClaimVerdict, len(verdicts))
	for i, v := range verdicts {
		norm := v
		switch v.Verdict {
		case VerdictSupported, VerdictContradicted, VerdictUnsupported:
			// 合法枚举保持。
		default:
			norm.Verdict = VerdictUnsupported
		}
		if norm.Risk < 0 {
			norm.Risk = 0
		}
		if norm.Risk > 5 {
			norm.Risk = 5
		}
		out[i] = norm
	}
	return out
}
