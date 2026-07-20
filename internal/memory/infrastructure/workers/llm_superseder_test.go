package workers_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	llminfra "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type completionClientFunc func(context.Context, *memport.CompletionRequest) (*memport.CompletionResponse, error)

func (f completionClientFunc) Complete(ctx context.Context, req *memport.CompletionRequest) (*memport.CompletionResponse, error) {
	return f(ctx, req)
}

type testGatewayAdapter struct{ gateway *llminfra.Gateway }

func (a testGatewayAdapter) Complete(ctx context.Context, req *memport.CompletionRequest) (*memport.CompletionResponse, error) {
	messages := make([]llminfra.Message, len(req.Messages))
	for i, message := range req.Messages {
		messages[i] = llminfra.Message{Role: message.Role, Content: message.Content}
	}
	response, err := a.gateway.Complete(ctx, &llminfra.CompletionRequest{
		Model: req.Model, Messages: messages, Temperature: float32(req.Temperature), MaxTokens: req.MaxTokens,
	})
	if err != nil {
		return nil, err
	}
	return &memport.CompletionResponse{Content: response.Content}, nil
}

func TestResolvingLLMSupersederUsesCurrentTenantClientOnEveryCall(t *testing.T) {
	var resolved, calledA, calledB int
	clientA := completionClientFunc(func(context.Context, *memport.CompletionRequest) (*memport.CompletionResponse, error) {
		calledA++
		return &memport.CompletionResponse{Content: `{"supersedes":false,"reason":"a"}`}, nil
	})
	clientB := completionClientFunc(func(context.Context, *memport.CompletionRequest) (*memport.CompletionResponse, error) {
		calledB++
		return &memport.CompletionResponse{Content: `{"supersedes":true,"reason":"b"}`}, nil
	})
	resolver := func(context.Context, string) (workers.TenantLLMClient, error) {
		resolved++
		if resolved == 1 {
			return clientA, nil
		}
		return clientB, nil
	}

	judge := workers.NewResolvingLLMSuperseder("tenant-1", resolver)
	first, err := judge.JudgeSupersede(context.Background(), "old", "new")
	require.NoError(t, err)
	require.False(t, first.Supersedes)
	second, err := judge.JudgeSupersede(context.Background(), "old", "newer")
	require.NoError(t, err)
	require.True(t, second.Supersedes)
	require.Equal(t, 2, resolved)
	require.Equal(t, 1, calledA)
	require.Equal(t, 1, calledB)
}

func TestResolvingLLMSupersederRoutesThroughNewProviderGateway(t *testing.T) {
	qwenCalls, zhipuCalls := 0, 0
	completionServer := func(calls *int, supersedes bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			*calls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"supersedes\":` + map[bool]string{true: "true", false: "false"}[supersedes] + `,\"reason\":\"provider\"}"}}],"model":"fake-model"}`))
		}))
	}
	qwenServer := completionServer(&qwenCalls, false)
	defer qwenServer.Close()
	zhipuServer := completionServer(&zhipuCalls, true)
	defer zhipuServer.Close()

	qwenGateway := llminfra.NewGateway()
	qwenGateway.RegisterClient(llminfra.ProviderQwen, llminfra.NewQwenClientWithBase("fake-key-qwen", qwenServer.URL, zap.NewNop()))
	qwenGateway.SetDefault(llminfra.ProviderQwen)
	zhipuGateway := llminfra.NewGateway()
	zhipuGateway.RegisterClient(llminfra.ProviderZhipu, llminfra.NewZhipuClientWithBase("fake-key-zhipu", zhipuServer.URL, zap.NewNop()))
	zhipuGateway.SetDefault(llminfra.ProviderZhipu)
	resolved := 0
	judge := workers.NewResolvingLLMSuperseder("tenant-1", func(context.Context, string) (workers.TenantLLMClient, error) {
		resolved++
		if resolved == 1 {
			return testGatewayAdapter{gateway: qwenGateway}, nil
		}
		return testGatewayAdapter{gateway: zhipuGateway}, nil
	})

	first, err := judge.JudgeSupersede(context.Background(), "old", "new")
	require.NoError(t, err)
	require.False(t, first.Supersedes)
	second, err := judge.JudgeSupersede(context.Background(), "old", "newer")
	require.NoError(t, err)
	require.True(t, second.Supersedes)
	require.Equal(t, 1, qwenCalls)
	require.Equal(t, 1, zhipuCalls)
}

func TestResolvingLLMSupersederDoesNotReuseClientAfterResolverFailure(t *testing.T) {
	available := true
	calls := 0
	client := completionClientFunc(func(context.Context, *memport.CompletionRequest) (*memport.CompletionResponse, error) {
		calls++
		return &memport.CompletionResponse{Content: `{"supersedes":false,"reason":"ok"}`}, nil
	})
	resolver := func(context.Context, string) (workers.TenantLLMClient, error) {
		if !available {
			return nil, errors.New("resolver unavailable")
		}
		return client, nil
	}
	judge := workers.NewResolvingLLMSuperseder("tenant-1", resolver)

	_, err := judge.JudgeSupersede(context.Background(), "old", "new")
	require.NoError(t, err)
	available = false
	_, err = judge.JudgeSupersede(context.Background(), "old", "new")
	require.ErrorContains(t, err, "resolve tenant llm")
	require.Equal(t, 1, calls)
	available = true
	_, err = judge.JudgeSupersede(context.Background(), "old", "new")
	require.NoError(t, err)
	require.Equal(t, 2, calls)
}

func TestResolvingLLMSupersederPropagatesContextCancellationBeforeClientCall(t *testing.T) {
	clientCalls := 0
	resolver := func(ctx context.Context, _ string) (workers.TenantLLMClient, error) {
		return nil, ctx.Err()
	}
	judge := workers.NewResolvingLLMSuperseder("tenant-1", resolver)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := judge.JudgeSupersede(ctx, "old", "new")
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, clientCalls)
}
