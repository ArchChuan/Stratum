package wiring

import (
	"context"
	"strings"
	"testing"

	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	mechanismdomain "github.com/byteBuilderX/stratum/internal/mechanism/domain"
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

// TestFactCheckJudgeUsesBaselineJudgeModelAndPrompt 验证 factcheck judge 请求
// 携带机制基线的 JudgeModel 与 Prompts.AgentFactCheck（profile 优先，构造值
// 仅作解析 key 与兜底）。
func TestFactCheckJudgeUsesBaselineJudgeModelAndPrompt(t *testing.T) {
	completer := &judgeCompleterStub{}
	baseline := func(context.Context, string) (mechanismdomain.Baseline, error) {
		return mechanismdomain.Baseline{
			Models: mechanismdomain.BaselineModels{JudgeModel: "qwen-max"},
			Prompts: mechanismdomain.BaselinePrompts{
				AgentFactCheck: "判定模板 %s 与 %s",
			},
		}, nil
	}
	judge := factCheckJudge{completer: completer, model: "qwen-turbo", baseline: baseline}

	if _, err := judge.JudgeClaims(context.Background(), []string{"c"}, "evidence"); err != nil {
		t.Fatal(err)
	}
	if completer.model != "qwen-max" {
		t.Fatalf("request model = %q, want profile JudgeModel %q", completer.model, "qwen-max")
	}
	user := completer.messages[1].Content
	if !strings.Contains(user, "判定模板") || !strings.Contains(user, "evidence") {
		t.Fatalf("user message must render profile template, got %q", user)
	}
}

// TestFactCheckJudgeFallsBackToSeedWithoutBaseline 验证 baseline 缺失（DB
// 不可用）时 model 用构造 env 值、模板用机制 seed——改造前现状不回归。
func TestFactCheckJudgeFallsBackToSeedWithoutBaseline(t *testing.T) {
	completer := &judgeCompleterStub{}
	judge := factCheckJudge{completer: completer, model: "qwen-turbo"}

	if _, err := judge.JudgeClaims(context.Background(), []string{"c"}, "evidence"); err != nil {
		t.Fatal(err)
	}
	if completer.model != "qwen-turbo" {
		t.Fatalf("request model = %q, want constructed fallback %q", completer.model, "qwen-turbo")
	}
	user := completer.messages[1].Content
	if !strings.Contains(user, "对下列每条 claim") {
		t.Fatalf("user message must carry seed template, got %q", user)
	}
}

// TestFactCheckJudgeKeepsFallbackOnBaselineError 验证基线解析失败（DB 故障）
// 时保持构造 model + seed 模板——judge advisory、profile 是配置源，不 fail-closed。
func TestFactCheckJudgeKeepsFallbackOnBaselineError(t *testing.T) {
	completer := &judgeCompleterStub{}
	baseline := func(context.Context, string) (mechanismdomain.Baseline, error) {
		return mechanismdomain.Baseline{}, context.DeadlineExceeded
	}
	judge := factCheckJudge{completer: completer, model: "qwen-turbo", baseline: baseline}

	if _, err := judge.JudgeClaims(context.Background(), []string{"c"}, "evidence"); err != nil {
		t.Fatal(err)
	}
	if completer.model != "qwen-turbo" {
		t.Fatalf("request model = %q, want fallback %q on baseline error", completer.model, "qwen-turbo")
	}
	if !strings.Contains(completer.messages[1].Content, "对下列每条 claim") {
		t.Fatalf("user message must keep seed template on baseline error, got %q", completer.messages[1].Content)
	}
}
