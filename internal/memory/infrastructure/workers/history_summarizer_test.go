package workers_test

import (
	"context"
	"errors"
	"testing"

	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
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
		return completionClientFunc(func(context.Context, *memport.CompletionRequest) (*memport.CompletionResponse, error) {
			return &memport.CompletionResponse{Content: label}, nil
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
		return completionClientFunc(func(context.Context, *memport.CompletionRequest) (*memport.CompletionResponse, error) {
			calls++
			return &memport.CompletionResponse{Content: "recovered"}, nil
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
