package workers

import (
	"context"
	"fmt"
	"strings"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

type historyLLM = TenantLLMClient

type LLMHistorySummarizer struct {
	llm      historyLLM
	tenantID string
	resolver TenantLLMResolver
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
	prompt := "Summarize this bounded period of user history. Preserve decisions, goals, preferences, and durable context; omit secrets and raw payloads.\n\n" + strings.Join(items, "\n")
	resp, err := client.Complete(ctx, &llmgateway.CompletionRequest{Messages: []llmgateway.Message{{Role: "user", Content: prompt}}, Temperature: .2})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Content), nil
}

func (s *LLMHistorySummarizer) CompressHistory(ctx context.Context, items []string) (string, error) {
	return s.SummarizeHistory(ctx, items)
}
