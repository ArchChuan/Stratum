package workers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
	"github.com/stretchr/testify/require"
)

type completionClientFunc func(context.Context, *memport.CompletionRequest) (*memport.CompletionResponse, error)

func (f completionClientFunc) Complete(ctx context.Context, req *memport.CompletionRequest) (*memport.CompletionResponse, error) {
	return f(ctx, req)
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

	clientA := completionClientFunc(func(ctx context.Context, req *memport.CompletionRequest) (*memport.CompletionResponse, error) {
		return callCompletionServer(ctx, qwenServer.URL, req)
	})
	clientB := completionClientFunc(func(ctx context.Context, req *memport.CompletionRequest) (*memport.CompletionResponse, error) {
		return callCompletionServer(ctx, zhipuServer.URL, req)
	})
	resolved := 0
	judge := workers.NewResolvingLLMSuperseder("tenant-1", func(context.Context, string) (workers.TenantLLMClient, error) {
		resolved++
		if resolved == 1 {
			return clientA, nil
		}
		return clientB, nil
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

func callCompletionServer(ctx context.Context, baseURL string, req *memport.CompletionRequest) (*memport.CompletionResponse, error) {
	body := map[string]interface{}{
		"model":    req.Model,
		"messages": []map[string]string{},
	}
	for _, m := range req.Messages {
		body["messages"] = append(body["messages"].([]map[string]string), map[string]string{"role": m.Role, "content": m.Content})
	}
	b, _ := json.Marshal(body)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", baseURL+"/v1/chat/completions", bytes.NewReader(b))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer test-key")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &memport.CompletionResponse{Content: result.Choices[0].Message.Content}, nil
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
