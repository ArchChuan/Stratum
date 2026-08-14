package wiring

import (
	"context"
	"strings"
	"testing"

	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
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
// 请求 model 用构造 env 值、user 模板走内联硬编码（pre-refactor 行为）。
func TestFactCheckJudgeUsesConstructedModelAndBuiltinTemplate(t *testing.T) {
	completer := &judgeCompleterStub{}
	judge := factCheckJudge{completer: completer, model: "qwen-turbo"}

	if _, err := judge.JudgeClaims(context.Background(), []string{"c"}, "evidence"); err != nil {
		t.Fatal(err)
	}
	if completer.model != "qwen-turbo" {
		t.Fatalf("request model = %q, want constructed %q", completer.model, "qwen-turbo")
	}
	user := completer.messages[1].Content
	if !strings.Contains(user, "对下列每条 claim") || !strings.Contains(user, "evidence") {
		t.Fatalf("user message must carry builtin template, got %q", user)
	}
	if got := completer.messages[0].Content; got != "你是严谨的事实核查法官。只输出 JSON，不输出其他内容。" {
		t.Fatalf("system prompt must stay the judge invariant, got %q", got)
	}
}
