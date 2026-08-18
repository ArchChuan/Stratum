package workers

import (
	"context"
	"fmt"
	"strings"

	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

type historyLLM = TenantLLMClient

type LLMHistorySummarizer struct {
	llm           historyLLM
	tenantID      string
	resolver      TenantLLMResolver
	paramResolver memport.PlatformParamResolver
}

var _ HistorySummarizer = (*LLMHistorySummarizer)(nil)
var _ HistoryCompressor = (*LLMHistorySummarizer)(nil)

func NewLLMHistorySummarizer(llm historyLLM) *LLMHistorySummarizer {
	return &LLMHistorySummarizer{llm: llm}
}

// NewResolvingLLMHistorySummarizer resolves the tenant client for every operation.
func NewResolvingLLMHistorySummarizer(tenantID string, resolver TenantLLMResolver) *LLMHistorySummarizer {
	return &LLMHistorySummarizer{tenantID: tenantID, resolver: resolver}
}

// WithParamResolver sets the platform parameter resolver used to resolve
// per-call summary model/temperature/prompt. A nil resolver keeps the const
// defaults.
func (s *LLMHistorySummarizer) WithParamResolver(r memport.PlatformParamResolver) *LLMHistorySummarizer {
	s.paramResolver = r
	return s
}

func (s *LLMHistorySummarizer) SummarizeHistory(ctx context.Context, items []string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("history llm unavailable")
	}
	client := s.llm
	if s.resolver != nil {
		resolved, err := resolveTenantLLM(ctx, s.tenantID, s.resolver)
		if err != nil {
			return "", err
		}
		client = resolved
	}
	if client == nil {
		return "", fmt.Errorf("history llm unavailable")
	}
	model := resolvePlatformString(ctx, s.paramResolver, "memory.history_summary_model", "")
	temperature := resolvePlatformFloat(ctx, s.paramResolver, "memory.history_summary_temperature", constants.TaskSummarizeTemperature)
	prompt := resolvePlatformString(ctx, s.paramResolver, "memory.history_summary_prompt", constants.MemoryHistorySummaryDefaultPrompt)
	req := llmdomain.NewSummarizeRequest(model, prompt, items, 0)
	// NewSummarizeRequest 内部固定 TaskSummarizeTemperature；平台配置的温度
	// 在构造后覆盖（0 = 保留默认）。
	if temperature != 0 {
		v := float64(temperature)
		req.Temperature = &v
	}
	resp, err := client.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Content), nil
}

func (s *LLMHistorySummarizer) CompressHistory(ctx context.Context, items []string) (string, error) {
	return s.SummarizeHistory(ctx, items)
}
