package workers

import (
	"context"
	"fmt"
	"strings"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

type historyLLM interface {
	Complete(context.Context, *llmgateway.CompletionRequest) (*llmgateway.CompletionResponse, error)
}

type LLMHistorySummarizer struct{ llm historyLLM }

var _ HistorySummarizer = (*LLMHistorySummarizer)(nil)
var _ HistoryCompressor = (*LLMHistorySummarizer)(nil)

func NewLLMHistorySummarizer(llm historyLLM) *LLMHistorySummarizer {
	return &LLMHistorySummarizer{llm: llm}
}

func (s *LLMHistorySummarizer) SummarizeHistory(ctx context.Context, items []string) (string, error) {
	if s == nil || s.llm == nil {
		return "", fmt.Errorf("history llm unavailable")
	}
	prompt := "Summarize this bounded period of user history. Preserve decisions, goals, preferences, and durable context; omit secrets and raw payloads.\n\n" + strings.Join(items, "\n")
	resp, err := s.llm.Complete(ctx, &llmgateway.CompletionRequest{Messages: []llmgateway.Message{{Role: "user", Content: prompt}}, Temperature: .2})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Content), nil
}

func (s *LLMHistorySummarizer) CompressHistory(ctx context.Context, items []string) (string, error) {
	return s.SummarizeHistory(ctx, items)
}
