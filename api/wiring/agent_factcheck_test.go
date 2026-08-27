package wiring

import (
	"context"
	"strings"
	"testing"

	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/stretchr/testify/require"
)

// judgeCompleterStub 捕获 factCheckJudge 发出的请求，返回可解析的判定 JSON。
type judgeCompleterStub struct {
	model    string
	messages []llmgatewaydomain.Message
}

func (s *judgeCompleterStub) Complete(_ context.Context, req *llmgatewaydomain.CompletionRequest) (*llmgatewaydomain.CompletionResponse, error) {
	s.model = req.Model
	s.messages = req.Messages
	return &llmgatewaydomain.CompletionResponse{
		Content: `{"claims":[{"text":"c","verdict":"SUPPORTED","risk":1}]}`,
	}, nil
}

func (s *judgeCompleterStub) CompleteStream(context.Context, *llmgatewaydomain.CompletionRequest, func(string)) (*llmgatewaydomain.CompletionResponse, error) {
	return nil, nil
}

// TestFactCheckJudgeUsesConstructedModelAndBuiltinTemplate 验证 factcheck judge
// 请求 model 和 system prompt 均来自构造时传入的参数，user 消息承载程序填充的 claims/evidence。
func TestFactCheckJudgeUsesConstructedModelAndBuiltinTemplate(t *testing.T) {
	completer := &judgeCompleterStub{}
	judge := factCheckJudge{completer: completer, model: "qwen-turbo", prompt: "你是严谨的事实核查法官。只输出 JSON。"}

	if _, err := judge.JudgeClaims(context.Background(), []string{"c"}, "evidence"); err != nil {
		t.Fatal(err)
	}
	if completer.model != "qwen-turbo" {
		t.Fatalf("request model = %q, want constructed %q", completer.model, "qwen-turbo")
	}
	// system prompt 是构造传入的纯规则文本
	if got := completer.messages[0].Content; got != "你是严谨的事实核查法官。只输出 JSON。" {
		t.Fatalf("system prompt must match constructed value, got %q", got)
	}
	// user 消息承载程序填充的 claims 和 evidence
	user := completer.messages[1].Content
	if !strings.Contains(user, "Claims:") || !strings.Contains(user, "evidence") {
		t.Fatalf("user message must carry program-filled claims and evidence, got %q", user)
	}
}

// fakeFCR 是参数解析器 stub：按预置 key→value 映射返回，未配置的 key 视为未设置。
type fakeFCR struct {
	values map[string]any
}

func (f fakeFCR) Resolve(_ context.Context, key string, _ map[string]any) (any, bool, error) {
	v, ok := f.values[key]
	return v, ok, nil
}

// TestResolveFactCheckSettings 覆盖对账/judge 平台配置解析分支：全关 → nil；
// citation-only → 无 Judge 的对账 settings；enabled+model → 完整 settings（含
// temperature）；enabled 但 model 空 / gateway 缺失 → fail-closed nil。
func TestResolveFactCheckSettings(t *testing.T) {
	gateway := &judgeCompleterStub{}

	t.Run("all off returns nil", func(t *testing.T) {
		got, err := resolveFactCheckSettings(gateway, fakeFCR{values: map[string]any{}}, nil)
		require.NoError(t, err)
		require.Nil(t, got)
	})

	t.Run("citation-only runs reconciliation without judge", func(t *testing.T) {
		got, err := resolveFactCheckSettings(gateway, fakeFCR{values: map[string]any{
			"agent.factcheck.citation_verify": true,
		}}, nil)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.False(t, got.Enabled)
		require.True(t, got.CitationVerify)
		require.Nil(t, got.Judge, "citation-only 不装配 judge")
	})

	t.Run("enabled builds full settings with temperature", func(t *testing.T) {
		got, err := resolveFactCheckSettings(gateway, fakeFCR{values: map[string]any{
			"agent.factcheck.enabled":           true,
			"agent.factcheck.judge.model":       "qwen-turbo",
			"agent.factcheck.judge.prompt":      "你是严谨的事实核查法官。",
			"agent.factcheck.top_k":             int64(5),
			"agent.factcheck.max_claims":        int64(3),
			"agent.factcheck.judge.temperature": 0.7,
		}}, nil)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.True(t, got.Enabled)
		require.Equal(t, 5, got.TopK)
		require.Equal(t, 3, got.MaxClaims)
		judge, ok := got.Judge.(factCheckJudge)
		require.True(t, ok)
		require.NotNil(t, judge.temperature)
		require.Equal(t, 0.7, *judge.temperature)
	})

	t.Run("enabled with zero temperature leaves judge default", func(t *testing.T) {
		got, err := resolveFactCheckSettings(gateway, fakeFCR{values: map[string]any{
			"agent.factcheck.enabled":      true,
			"agent.factcheck.judge.model":  "qwen-turbo",
			"agent.factcheck.judge.prompt": "prompt",
		}}, nil)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Nil(t, got.Judge.(factCheckJudge).temperature)
	})

	t.Run("enabled without model is fail-closed nil", func(t *testing.T) {
		got, err := resolveFactCheckSettings(gateway, fakeFCR{values: map[string]any{
			"agent.factcheck.enabled": true,
		}}, nil)
		require.NoError(t, err)
		require.Nil(t, got)
	})

	t.Run("enabled without gateway is fail-closed nil", func(t *testing.T) {
		got, err := resolveFactCheckSettings(nil, fakeFCR{values: map[string]any{
			"agent.factcheck.enabled":      true,
			"agent.factcheck.judge.model":  "qwen-turbo",
			"agent.factcheck.judge.prompt": "prompt",
		}}, nil)
		require.NoError(t, err)
		require.Nil(t, got)
	})
}
