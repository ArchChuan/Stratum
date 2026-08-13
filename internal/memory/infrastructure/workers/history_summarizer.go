package workers

import (
	"context"
	"fmt"
	"strings"

	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

// summarizePrefix 是周期总结指令前缀兜底（现状硬编码值）。机制基线建档后
// 由 wiring 注入覆盖，空值维持现状行为。
const summarizePrefix = "Summarize this bounded period of user history. Preserve decisions, goals, preferences, and durable context; omit secrets and raw payloads.\n\n"

type historyLLM = TenantLLMClient

type LLMHistorySummarizer struct {
	llm           historyLLM
	tenantID      string
	resolver      TenantLLMResolver
	summarizeTmpl string
	summaryModel  string
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

// WithSummarizePrompt overrides the summarization instruction with the
// mechanism baseline prompt. Empty keeps summarizePrefix.
func (s *LLMHistorySummarizer) WithSummarizePrompt(p string) *LLMHistorySummarizer {
	s.summarizeTmpl = p
	return s
}

// WithSummaryModel sets the summarization model from the mechanism baseline
// (MEMORY_SUMMARY_MODEL 兜底值经 wiring 注入；基线优先覆盖).
// Empty keeps the client's default resolution (pre-change behavior).
func (s *LLMHistorySummarizer) WithSummaryModel(m string) *LLMHistorySummarizer {
	s.summaryModel = m
	return s
}

// summarizePrefixOr 返回生效指令前缀：基线注入值优先，空则兜底内置常量。
func (s *LLMHistorySummarizer) summarizePrefixOr() string {
	if s.summarizeTmpl != "" {
		return s.summarizeTmpl
	}
	return summarizePrefix
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
	prompt := s.summarizePrefixOr() + strings.Join(items, "\n")
	resp, err := client.Complete(ctx, &memport.CompletionRequest{
		Model:       s.summaryModel,
		Messages:    []memport.CompletionMessage{{Role: "user", Content: prompt}},
		Temperature: .2,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Content), nil
}

func (s *LLMHistorySummarizer) CompressHistory(ctx context.Context, items []string) (string, error) {
	return s.SummarizeHistory(ctx, items)
}
