package workers_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
	"github.com/stretchr/testify/require"
)

func TestResolvingHistoryProcessorResolvesForSummarizeAndCompress(t *testing.T) {
	resolved := 0
	resolver := func(context.Context, string) (workers.TenantLLMClient, error) {
		resolved++
		label := "summary-a"
		if resolved == 2 {
			label = "summary-b"
		}
		return completionClientFunc(func(context.Context, *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error) {
			return &llmdomain.CompletionResponse{Content: label}, nil
		}), nil
	}
	processor := workers.NewResolvingLLMHistorySummarizer("tenant-1", resolver)

	summary, err := processor.SummarizeHistory(context.Background(), []string{"one"})
	require.NoError(t, err)
	require.Equal(t, "summary-a", summary)
	compressed, err := processor.CompressHistory(context.Background(), []string{"two"})
	require.NoError(t, err)
	require.Equal(t, "summary-b", compressed)
	require.Equal(t, 2, resolved)
}

func TestResolvingHistoryProcessorRecoversWithoutReusingOldClient(t *testing.T) {
	available := false
	calls := 0
	resolver := func(context.Context, string) (workers.TenantLLMClient, error) {
		if !available {
			return nil, errors.New("temporarily unavailable")
		}
		return completionClientFunc(func(context.Context, *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error) {
			calls++
			return &llmdomain.CompletionResponse{Content: "recovered"}, nil
		}), nil
	}
	processor := workers.NewResolvingLLMHistorySummarizer("tenant-1", resolver)

	_, err := processor.SummarizeHistory(context.Background(), []string{"one"})
	require.ErrorContains(t, err, "resolve tenant llm")
	require.Zero(t, calls)
	available = true
	summary, err := processor.SummarizeHistory(context.Background(), []string{"one"})
	require.NoError(t, err)
	require.Equal(t, "recovered", summary)
	require.Equal(t, 1, calls)
}

// TestHistoryProcessorUsesFallbackSummarizePrompt 验证周期总结指令走内置前缀
// （mechanism 移除后为唯一权威）。
func TestHistoryProcessorUsesFallbackSummarizePrompt(t *testing.T) {
	var got string
	client := completionClientFunc(func(_ context.Context, req *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error) {
		got = req.Messages[0].Content
		return &llmdomain.CompletionResponse{Content: "s"}, nil
	})
	processor := workers.NewLLMHistorySummarizer(client)

	if _, err := processor.SummarizeHistory(context.Background(), []string{"item"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "Summarize this bounded period") {
		t.Fatalf("fallback prefix missing: %q", got)
	}
}

// TestHistoryProcessorLeavesModelEmpty 验证总结请求 Model 为空（llmgateway
// client 默认解析，pre-refactor 行为；金丝雀回归）。
func TestHistoryProcessorLeavesModelEmpty(t *testing.T) {
	var gotModel string
	client := completionClientFunc(func(_ context.Context, req *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error) {
		gotModel = req.Model
		return &llmdomain.CompletionResponse{Content: "s"}, nil
	})
	processor := workers.NewLLMHistorySummarizer(client)

	if _, err := processor.SummarizeHistory(context.Background(), []string{"item"}); err != nil {
		t.Fatal(err)
	}
	if gotModel != "" {
		t.Fatalf("expected empty model by default, got %q", gotModel)
	}
}
