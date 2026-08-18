package infrastructure

import (
	"fmt"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// estimateMessagesTokens 确定性估算消息 token 数（bytes/3，英文方向约 33%
// 高估 = 保守 fail-closed 侧）。与 agent 预算链估算算法无关，网关层独立。
func estimateMessagesTokens(msgs []domain.Message) int {
	total := 0
	for _, m := range msgs {
		total += len(m.Role) + len(m.Content)
	}
	return total/3 + 1
}

// EnforceModelPolicy 对单次尝试副本应用模型权威策略（纯函数，绝不修改共享
// req）。执行顺序固定：floor（仅 reasoning）→ L1 clamp/注入 → 采样注入 →
// L2 窗口预检 → L3 采样校验 → L4 能力校验。
//
//   - floor：推理模型显式 max_tokens 低于平台 DefaultOutputReserveTokens 的
//     值抬升到兜底（思考长度模型自定，低于兜底几乎必然是截断事故）。
//   - L1：请求值超模型上限 → clamp；请求 0（未设置）→ 注入模型值（>0）。
//   - 采样注入：请求未显式设置 temperature → 模型 sampling_params 默认。
//   - L2：估算消息 + 有效 max_tokens > context_window → 拒（窗口 0 = 未知跳过）。
//   - L3：temperature 越界 → 拒（max_temperature=0 = 不支持温度）。
//   - L4：tools 请求但模型能力集非空且不含 tool_use（known-non）→ 拒；
//     unknown（policy nil / 空能力集）放行，由接线层短路。
//
// 返回 nil 错误时返回克隆副本（无变化返回原 req）；拦截时返回 (nil, 错误)，
// 拦截错误是 permanent（errors.Is 可匹配 domain sentinel）。
func EnforceModelPolicy(req *CompletionRequest, p *ModelPolicy, reasoning bool) (*CompletionRequest, error) {
	if p == nil {
		// 权威数据不存在：L1-L3 全部跳过，不做任何治理（接线层已计
		// IncPolicyMissing，此处纯函数兜底防御）。
		return req, nil
	}
	cloned := *req
	cloned.Model = req.Model
	changed := false
	maxTokens := req.MaxTokens

	// floor（仅 reasoning）：先于 L1，保证「先 floor 后 clamp」原子性。
	if reasoning && maxTokens > 0 && maxTokens < constants.DefaultOutputReserveTokens {
		maxTokens = constants.DefaultOutputReserveTokens
		changed = true
	}
	// L1 clamp/注入：模型上限是硬约束，floored 值若超上限同样被压回。
	if p.MaxTokens > 0 {
		if maxTokens > p.MaxTokens {
			maxTokens = p.MaxTokens
			changed = true
		} else if maxTokens <= 0 {
			maxTokens = p.MaxTokens
			changed = true
		}
	}
	cloned.MaxTokens = maxTokens

	// 采样注入：请求未显式设置 → 模型 sampling_params 默认（Provider 级
	// default_sampling 在 Task 7 注入链末端）。
	temp := req.Temperature
	if temp == nil && p.SamplingDefaults != nil && p.SamplingDefaults.Temperature != nil {
		v := *p.SamplingDefaults.Temperature
		temp = &v
		changed = true
	}
	cloned.Temperature = temp

	// L2 窗口预检：context_window=0（未知）跳过；估算 + 有效 max_tokens
	// 超窗口 → 拒。contextLengthExceededError 已带 Permanent()/ContextLengthExceeded()。
	if p.ContextWindow > 0 && maxTokens > 0 {
		estimated := estimateMessagesTokens(req.Messages)
		if estimated+maxTokens > p.ContextWindow {
			return nil, fmt.Errorf("llmgateway: %w (model %s, estimated %d + max_tokens %d > window %d)",
				ErrContextLengthExceeded, req.Model, estimated, maxTokens, p.ContextWindow)
		}
	}

	// L3 采样校验（注入后的有效值）：max_temperature=0 = 不支持温度 → 带值即拒；
	// 配置上限 → 超限拒；未配置 → 仅校验 [0,1] 合法性。
	if temp != nil {
		if p.MaxTemperature == nil {
			if *temp < 0 || *temp > 1 {
				return nil, fmt.Errorf("llmgateway: %w (temperature %v outside [0,1])",
					domain.ErrSamplingOutOfRange, *temp)
			}
		} else if *p.MaxTemperature == 0 {
			return nil, fmt.Errorf("llmgateway: %w (temperature not supported, max_temperature=0)",
				domain.ErrSamplingOutOfRange)
		} else if *temp > *p.MaxTemperature {
			return nil, fmt.Errorf("llmgateway: %w (temperature %v exceeds max %v)",
				domain.ErrSamplingOutOfRange, *temp, *p.MaxTemperature)
		}
	}

	// L4 能力不匹配（tool_use）：能力集非空且不含 tool_use = known-non → 拒；
	// 空能力集 = unknown → 放行（spec §4：unknown 放行，fail-open 侧）。
	if len(req.Tools) > 0 && len(p.Capabilities) > 0 {
		hasToolUse := false
		for _, c := range p.Capabilities {
			if c == domain.CapToolUse {
				hasToolUse = true
				break
			}
		}
		if !hasToolUse {
			return nil, fmt.Errorf("llmgateway: %w (model %s lacks tool_use)",
				domain.ErrCapabilityUnsupported, req.Model)
		}
	}

	if !changed {
		return req, nil
	}
	return &cloned, nil
}
