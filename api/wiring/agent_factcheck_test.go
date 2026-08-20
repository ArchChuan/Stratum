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
