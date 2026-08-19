package infrastructure

import (
	"fmt"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// EnforceModelPolicy 对单次尝试副本应用模型权威策略（纯函数，绝不修改共享
// req）。执行顺序固定：floor（仅 reasoning）→ L1 clamp/默认注入 → 采样注入 →
// L3 采样校验 → L4 能力校验。
//
//   - floor：推理模型显式 max_tokens 低于平台 DefaultOutputReserveTokens 的
//     值抬升到兜底（思考长度模型自定，低于兜底几乎必然是截断事故）。
//   - L1：请求值超模型上限 → clamp；请求 0（未设置）→ 注入模型默认输出。
//   - 采样注入：请求未显式设置 temperature → 模型 sampling_params 默认。
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
	maxTokens, maxChanged := resolveMaxTokens(req.MaxTokens, p, reasoning)
	cloned.MaxTokens = maxTokens
	temp, tempChanged := injectTemperature(req.Temperature, p)
	cloned.Temperature = temp
	if err := validateTemperature(temp, p); err != nil {
		return nil, err
	}
	if err := validateToolCapability(req, p); err != nil {
		return nil, err
	}
	changed := maxChanged || tempChanged
	if !changed {
		return req, nil
	}
	return &cloned, nil
}

func resolveMaxTokens(requested int, policy *ModelPolicy, reasoning bool) (int, bool) {
	value := requested
	if reasoning && value > 0 && value < constants.DefaultOutputReserveTokens {
		value = constants.DefaultOutputReserveTokens
	}
	if policy.MaxTokens <= 0 {
		return value, value != requested
	}
	if value <= 0 {
		value = policy.DefaultOutputTokens
	}
	if value > policy.MaxTokens {
		value = policy.MaxTokens
	}
	return value, value != requested
}

func injectTemperature(requested *float64, policy *ModelPolicy) (*float64, bool) {
	if requested != nil {
		return requested, false
	}
	for _, defaults := range []*domain.SamplingParams{
		policy.SamplingDefaults, policy.ProviderSamplingDefaults,
	} {
		if defaults != nil && defaults.Temperature != nil {
			value := *defaults.Temperature
			return &value, true
		}
	}
	return nil, false
}

func validateTemperature(temp *float64, policy *ModelPolicy) error {
	if temp == nil {
		return nil
	}
	if policy.MaxTemperature == nil {
		if *temp < 0 || *temp > 1 {
			return fmt.Errorf("llmgateway: %w (temperature %v outside [0,1])",
				domain.ErrSamplingOutOfRange, *temp)
		}
		return nil
	}
	if *policy.MaxTemperature == 0 {
		return fmt.Errorf("llmgateway: %w (temperature not supported, max_temperature=0)",
			domain.ErrSamplingOutOfRange)
	}
	if *temp > *policy.MaxTemperature {
		return fmt.Errorf("llmgateway: %w (temperature %v exceeds max %v)",
			domain.ErrSamplingOutOfRange, *temp, *policy.MaxTemperature)
	}
	return nil
}

func validateToolCapability(req *CompletionRequest, policy *ModelPolicy) error {
	if len(req.Tools) == 0 || len(policy.Capabilities) == 0 {
		return nil
	}
	for _, capability := range policy.Capabilities {
		if capability == domain.CapToolUse {
			return nil
		}
	}
	return fmt.Errorf("llmgateway: %w (model %s lacks tool_use)",
		domain.ErrCapabilityUnsupported, req.Model)
}
