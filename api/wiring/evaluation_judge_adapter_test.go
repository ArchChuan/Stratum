package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	evalport "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	parametersapp "github.com/byteBuilderX/stratum/internal/parameters/application"
	parametersdomain "github.com/byteBuilderX/stratum/internal/parameters/domain"
	"github.com/byteBuilderX/stratum/internal/parameters/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/stretchr/testify/require"
)

type fakeJudgeCompleter struct {
	response *llmgatewaydomain.CompletionResponse
	err      error
	got      *llmgatewaydomain.CompletionRequest
	calls    []*llmgatewaydomain.CompletionRequest
}

func (f *fakeJudgeCompleter) Complete(_ context.Context, req *llmgatewaydomain.CompletionRequest) (*llmgatewaydomain.CompletionResponse, error) {
	f.got = req
	f.calls = append(f.calls, req)
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

	require.Empty(t, completer.got.Model, "模型默认必须为空：交由 llmgateway 从模型目录解析，代码内不写死兜底模型")
	require.Nil(t, completer.got.Temperature) // judge 温度 0 = unset，留给模型默认注入
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

func TestJudgeAdapterJudgeAppendsToolSequenceWhenPresent(t *testing.T) {
	completer := &fakeJudgeCompleter{
		response: &llmgatewaydomain.CompletionResponse{Content: `{"passed": true, "reason": "ok"}`},
	}
	adapter := judgeAdapter{completer: completer}
	_, err := adapter.Judge(context.Background(), evalport.JudgeRequest{
		Input: "question", ExpectedOutput: "answer", Actual: "reply",
		ToolSequence: "tool_a\ncall 1\ntool_b\ncall 2",
	})
	require.NoError(t, err)
	require.Contains(t, completer.got.Messages[1].Content, "\n\nTool sequence:\ntool_a\ncall 1\ntool_b\ncall 2")
}

func TestJudgeAdapterJudgeOmitsToolSequenceWhenEmpty(t *testing.T) {
	completer := &fakeJudgeCompleter{
		response: &llmgatewaydomain.CompletionResponse{Content: `{"passed": true, "reason": "ok"}`},
	}
	adapter := judgeAdapter{completer: completer}
	_, err := adapter.Judge(context.Background(), evalport.JudgeRequest{
		Input: "question", ExpectedOutput: "answer", Actual: "reply",
	})
	require.NoError(t, err)
	want := "Rubric:\n" + judgeDefaultRubric +
		"\n\nInput:\nquestion" +
		"\n\nExpected output:\nanswer" +
		"\n\nActual output:\nreply"
	require.Equal(t, want, completer.got.Messages[1].Content)
}

// evalSnapshotWith 构造带 evaluation 组快照（VersionSeq=5）的 ctx，模拟评测 run
// 创建时点固化的快照注入。
func evalSnapshotWith(t *testing.T, values map[string]any) context.Context {
	t.Helper()
	snap := &domain.EvaluationContextSnapshot{
		SchemaVersion: domain.SnapshotSchemaVersion,
		Evaluation:    domain.GroupSnapshot{GroupKey: domain.GroupEvaluation, VersionSeq: 5, Values: values},
	}
	return domain.WithEvalSnapshot(context.Background(), snap)
}

// newJudgeParamsWithModel 构造真实 parameters 服务，平台快照写入 evaluation.judge.model，
// 用于快照缺失时 judgeModel 的平台值回退分支。
func newJudgeParamsWithModel(t *testing.T, model string) *parametersapp.Service {
	t.Helper()
	encoded, err := json.Marshal(model)
	require.NoError(t, err)
	store := &fakePlatformStore{values: map[string]string{"evaluation.judge.model": string(encoded)}}
	return parametersapp.NewService(parametersdomain.NewParametersRegistry(), store)
}

func TestJudgeAdapterSnapshotPreferred(t *testing.T) {
	cases := []struct {
		name   string
		ctx    context.Context
		params *parametersapp.Service
		want   string // 期望实际使用的 model
	}{
		{
			name: "snapshot overrides platform",
			ctx:  evalSnapshotWith(t, map[string]any{"evaluation.judge.model": "snapshot-model"}),
			want: "snapshot-model",
		},
		{
			name: "snapshot empty falls through",
			ctx:  evalSnapshotWith(t, nil),
			want: "",
		},
		{
			name:   "no snapshot falls back to platform values",
			ctx:    context.Background(),
			params: newJudgeParamsWithModel(t, "platform-model"),
			want:   "platform-model",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			completer := &fakeJudgeCompleter{
				response: &llmgatewaydomain.CompletionResponse{Content: `{"passed":true,"score":80,"reason":"ok"}`},
			}
			j := judgeAdapter{completer: completer, params: tc.params}
			_, err := j.Judge(tc.ctx, evalport.JudgeRequest{Input: "i", ExpectedOutput: "e", Actual: "a"})
			require.NoError(t, err)
			require.Equal(t, tc.want, j.judgeModel(tc.ctx, ""))
			require.Len(t, completer.calls, 1)
			require.Equal(t, tc.want, completer.calls[0].Model)
		})
	}
}

func TestJudgeAdapterEnabledFromSnapshot(t *testing.T) {
	completer := &fakeJudgeCompleter{
		response: &llmgatewaydomain.CompletionResponse{Content: `{"passed":true,"reason":"ok"}`},
	}
	j := judgeAdapter{completer: completer, params: nil}
	require.True(t, j.Enabled(evalSnapshotWith(t, map[string]any{"evaluation.judge.enabled": true})))
	require.False(t, j.Enabled(evalSnapshotWith(t, map[string]any{"evaluation.judge.enabled": false})))
	require.False(t, j.Enabled(evalSnapshotWith(t, nil))) // 空快照 → false（fail-closed）
	require.False(t, j.Enabled(context.Background()))     // 无快照 + params nil → false
}

func TestJudgeAdapterTemperatureFromSnapshot(t *testing.T) {
	completer := &fakeJudgeCompleter{
		response: &llmgatewaydomain.CompletionResponse{Content: `{"passed":true,"reason":"ok"}`},
	}
	j := judgeAdapter{completer: completer, params: nil}
	ctx := evalSnapshotWith(t, map[string]any{"evaluation.judge.temperature": float64(0.7)})
	require.Equal(t, float32(0.7), j.judgeTemperature(ctx))
	require.Zero(t, j.judgeTemperature(evalSnapshotWith(t, nil))) // 快照缺 temperature → 0（unset）
}

func TestObservationSnapshotPreferred(t *testing.T) {
	require.True(t, observationEnabled(evalSnapshotWith(t, map[string]any{"evaluation.observe.enabled": true}), nil))
	require.False(t, observationEnabled(evalSnapshotWith(t, map[string]any{"evaluation.observe.enabled": false}), nil))
	require.False(t, observationEnabled(evalSnapshotWith(t, nil), nil)) // 空快照 → false（fail-closed）

	rateCtx := evalSnapshotWith(t, map[string]any{"evaluation.observe.sample_rate": float64(0.5)})
	require.Equal(t, 0.5, observationSampleRate(rateCtx, nil))
	// 越界采样率回退常量默认
	invalidCtx := evalSnapshotWith(t, map[string]any{"evaluation.observe.sample_rate": float64(1.5)})
	require.Equal(t, constants.ObservationSampleRateDefault, observationSampleRate(invalidCtx, nil))
	require.Equal(t, constants.ObservationSampleRateDefault, observationSampleRate(evalSnapshotWith(t, nil), nil))

	seq, ok, err := observationPlatformVersion(evalSnapshotWith(t, nil), nil)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(5), seq) // evalSnapshotWith 固定 VersionSeq=5
}

func TestObservationFallsBackToPlatformValues(t *testing.T) {
	store := &fakePlatformStore{
		values: map[string]string{
			"evaluation.observe.enabled":     `true`,
			"evaluation.observe.sample_rate": `0.2`,
		},
		versions: map[string][]port.PlatformVersion{
			constants.PlatformGroupEvaluation: {
				{
					GroupKey: constants.PlatformGroupEvaluation, VersionSeq: 3, IsCurrent: true,
					Snapshot: map[string]json.RawMessage{},
				},
			},
		},
	}
	params := parametersapp.NewService(parametersdomain.NewParametersRegistry(), store)

	require.True(t, observationEnabled(context.Background(), params))
	require.Equal(t, 0.2, observationSampleRate(context.Background(), params))

	seq, ok, err := observationPlatformVersion(context.Background(), params)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(3), seq)
}
