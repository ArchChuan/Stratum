// Model window / output-reserve resolution.

package application

import (
	"context"

	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

func (s *AgentService) resolveExecutionWindow(
	ctx context.Context,
	tenantID, model string,
	explicit int,
) (int, agentgraph.WindowSource) {
	// 评测执行：执行窗口取评测 run 创建时点固化的快照值（D4），跳过运行时
	// 来源链，保证被测执行与创建时捕获的参数一致。
	if es := port.ExecutionSnapshotFromCtx(ctx); es != nil && es.ContextWindowTokens > 0 {
		return es.ContextWindowTokens, agentgraph.WindowSnapshot
	}
	modelWin, src := agentgraph.ResolveModelWindow(
		ctx, model, s.deps.ModelContextProvider, s.deps.VendorWindowLookup,
	)
	if modelWin > constants.MaxContextWindowTokens {
		modelWin = constants.MaxContextWindowTokens
	}
	window, agentSrc := agentgraph.ResolveAgentWindow(modelWin, explicit)
	if src == agentgraph.WindowVendorTable || src == agentgraph.WindowFallback {
		s.deps.Logger.Warn("agent: model window resolved from fallback source",
			zap.String("model", model), zap.String("source", string(src)),
			zap.Int("model_window", modelWin), zap.Int("window", window))
	}
	return window, agentSrc
}

// resolveOutputReserve 解析主模型输出预留（Spec 第 2 节 outputReserve 来源链）：
// 显式 cfg.MaxTokens（>0）> DB 模型 max_tokens（模型管理权威）> vendor 表
// maxOut > DefaultOutputReserveTokens。DB 权威插在链头：预留 < 实际发送
// max_tokens 时发送值会溢出上下文窗口被 provider 400 永久中止，预留必须
// 与 llmgateway L1 注入一致。局限：execution 级 effective-parameter 覆写
// 对 max_tokens 的调整在此不可见（service 层解析时以 agent 配置为准），
// 保守方向一致，不放大可用窗口。

func (s *AgentService) resolveOutputReserve(
	ctx context.Context, tenantID, model string, explicitMaxTokens int,
) int {
	// 评测执行：输出预留取评测 run 创建时点固化的快照值（D4），与窗口同源。
	if es := port.ExecutionSnapshotFromCtx(ctx); es != nil && es.OutputReserveTokens > 0 {
		return es.OutputReserveTokens
	}
	if explicitMaxTokens > 0 {
		return explicitMaxTokens
	}
	if reserve, ok := s.modelOutputReserve(ctx, tenantID, model); ok {
		return reserve
	}
	if s.deps.VendorWindowLookup != nil {
		if _, maxOut := s.deps.VendorWindowLookup(model); maxOut > 0 {
			return maxOut
		}
	}
	return constants.DefaultOutputReserveTokens
}

func (s *AgentService) modelOutputReserve(ctx context.Context, tenantID, model string) (int, bool) {
	if s.deps.ModelDetailsProvider == nil {
		return 0, false
	}
	details, err := s.deps.ModelDetailsProvider.ListTenantModelDetails(ctx, tenantID)
	if err != nil {
		return 0, false
	}
	for _, detail := range details {
		if detail.Model != model {
			continue
		}
		switch {
		case detail.EffectiveMaxTokens > 0:
			return detail.EffectiveMaxTokens, true
		case detail.MaxTokens > 0:
			return detail.MaxTokens, true
		case detail.DefaultOutputTokens > 0:
			return detail.DefaultOutputTokens, true
		default:
			return 0, false
		}
	}
	return 0, false
}
