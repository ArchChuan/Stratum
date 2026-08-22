package workers_test

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
)

// fakePlatformParamResolver 按 registry key 返回平台参数值。
type fakePlatformParamResolver struct {
	vals map[string]any
}

func (f fakePlatformParamResolver) ResolvePlatform(_ context.Context, key string) (any, bool, error) {
	v, ok := f.vals[key]
	return v, ok, nil
}

const testSupersedePrompt = "旧事实：%s\n新事实：%s\n只输出 JSON。"
const testHistorySummaryPrompt = "Summarize this period of user history."

func newTestSuperseder(client workers.TenantLLMClient) *workers.LLMSuperseder {
	return workers.NewLLMSuperseder(client).WithParamResolver(fakePlatformParamResolver{vals: map[string]any{
		"memory.supersede_prompt": testSupersedePrompt,
	}})
}

func newResolvingTestSuperseder(tenantID string, resolver workers.TenantLLMResolver) *workers.LLMSuperseder {
	return workers.NewResolvingLLMSuperseder(tenantID, resolver).WithParamResolver(fakePlatformParamResolver{vals: map[string]any{
		"memory.supersede_prompt": testSupersedePrompt,
	}})
}

func newTestHistorySummarizer(client workers.TenantLLMClient) *workers.LLMHistorySummarizer {
	return workers.NewLLMHistorySummarizer(client).WithParamResolver(fakePlatformParamResolver{vals: map[string]any{
		"memory.history_summary_prompt": testHistorySummaryPrompt,
	}})
}

func newResolvingTestHistorySummarizer(tenantID string, resolver workers.TenantLLMResolver) *workers.LLMHistorySummarizer {
	return workers.NewResolvingLLMHistorySummarizer(tenantID, resolver).WithParamResolver(fakePlatformParamResolver{vals: map[string]any{
		"memory.history_summary_prompt": testHistorySummaryPrompt,
	}})
}
