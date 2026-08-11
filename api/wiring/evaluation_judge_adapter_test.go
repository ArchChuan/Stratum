package wiring

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	evalport "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/stretchr/testify/require"
)

type fakeJudgeCompleter struct {
	response *llmgatewaydomain.CompletionResponse
	err      error
	got      *llmgatewaydomain.CompletionRequest
}

func (f *fakeJudgeCompleter) Complete(_ context.Context, req *llmgatewaydomain.CompletionRequest) (*llmgatewaydomain.CompletionResponse, error) {
	f.got = req
	return f.response, f.err
}

func (f *fakeJudgeCompleter) CompleteStream(context.Context, *llmgatewaydomain.CompletionRequest, func(string)) (*llmgatewaydomain.CompletionResponse, error) {
	return nil, errors.New("unused")
}

func TestJudgeAdapterEnabledFailsClosedWithoutParams(t *testing.T) {
	adapter := judgeAdapter{} // no params, no completer
	require.False(t, adapter.Enabled(context.Background()))
}

func TestJudgeAdapterJudgeFailsWithoutCompleter(t *testing.T) {
	adapter := judgeAdapter{}
	_, err := adapter.Judge(context.Background(), evalport.JudgeRequest{})
	require.ErrorContains(t, err, "no LLM completer")
}

func TestJudgeAdapterJudgeUsesPlatformDefaultsAndParsesVerdict(t *testing.T) {
	completer := &fakeJudgeCompleter{
		response: &llmgatewaydomain.CompletionResponse{Content: `{"passed": true, "reason": "回答完整"}`},
	}
	adapter := judgeAdapter{completer: completer} // nil params/prompts degrade to built-in defaults

	result, err := adapter.Judge(context.Background(), evalport.JudgeRequest{
		Input: "1", ExpectedOutput: "null", Actual: "2",
	})
	require.NoError(t, err)
	require.True(t, result.Passed)
	require.Equal(t, "回答完整", result.Message)

	require.Equal(t, "qwen-plus", completer.got.Model)
	require.Equal(t, float32(0), completer.got.Temperature)
	require.Equal(t, 1024, completer.got.MaxTokens)
	require.Contains(t, completer.got.Messages[1].Content, judgeDefaultRubric)
	require.Contains(t, completer.got.Messages[1].Content, "Input:\n1")
}

func TestJudgeAdapterJudgeHonorsExplicitModelAndRubric(t *testing.T) {
	completer := &fakeJudgeCompleter{
		response: &llmgatewaydomain.CompletionResponse{Content: `{"passed": false, "reason": "缺少来源"}`},
	}
	adapter := judgeAdapter{completer: completer}

	result, err := adapter.Judge(context.Background(), evalport.JudgeRequest{
		Model: "qwen-max", Rubric: "custom rubric", Actual: "x",
	})
	require.NoError(t, err)
	require.False(t, result.Passed)
	require.Equal(t, "qwen-max", completer.got.Model)
	require.NotContains(t, completer.got.Messages[1].Content, judgeDefaultRubric)
	require.Contains(t, completer.got.Messages[1].Content, "custom rubric")
}

func TestJudgeAdapterJudgePropagatesCompleterError(t *testing.T) {
	completer := &fakeJudgeCompleter{err: errors.New("provider timeout")}
	adapter := judgeAdapter{completer: completer}
	_, err := adapter.Judge(context.Background(), evalport.JudgeRequest{})
	require.ErrorContains(t, err, "provider timeout")
}

func TestJudgeAdapterJudgeRejectsInvalidVerdict(t *testing.T) {
	completer := &fakeJudgeCompleter{response: &llmgatewaydomain.CompletionResponse{Content: "not json"}}
	adapter := judgeAdapter{completer: completer}
	_, err := adapter.Judge(context.Background(), evalport.JudgeRequest{})
	require.ErrorContains(t, err, "parse verdict")
}

func TestParseJudgeResponseToleratesCodeFence(t *testing.T) {
	result, err := parseJudgeResponse("```json\n{\"passed\": false, \"reason\": \"r\"}\n```")
	require.NoError(t, err)
	require.False(t, result.Passed)
	require.Equal(t, "r", result.Message)
}

func TestJudgeAdapterJudgeBuildsRequestWithAllMaterial(t *testing.T) {
	completer := &fakeJudgeCompleter{
		response: &llmgatewaydomain.CompletionResponse{Content: `{"passed": true, "reason": "ok"}`},
	}
	adapter := judgeAdapter{completer: completer}
	_, err := adapter.Judge(context.Background(), evalport.JudgeRequest{
		Input: "question", ExpectedOutput: "answer", Actual: "reply",
	})
	require.NoError(t, err)
	body := strings.Join([]string{
		completer.got.Messages[0].Content, completer.got.Messages[1].Content,
	}, "\n")
	for _, want := range []string{"question", "answer", "reply"} {
		require.Contains(t, body, want)
	}
	require.Equal(t, domain.AssertionResult{Passed: true, Message: "ok"}, domain.AssertionResult{Passed: true, Message: "ok"})
}
