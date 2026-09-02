package wiring

import (
	"context"
	"strings"
	"testing"
	"time"

	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

type knowledgeJudgeCompleterStub struct {
	model    string
	messages []llmgatewaydomain.Message
	content  string
	err      error
}

func (s *knowledgeJudgeCompleterStub) Complete(_ context.Context, req *llmgatewaydomain.CompletionRequest) (*llmgatewaydomain.CompletionResponse, error) {
	s.model = req.Model
	s.messages = req.Messages
	if s.err != nil {
		return nil, s.err
	}
	return &llmgatewaydomain.CompletionResponse{Content: s.content}, nil
}

func (s *knowledgeJudgeCompleterStub) CompleteStream(context.Context, *llmgatewaydomain.CompletionRequest, func(string)) (*llmgatewaydomain.CompletionResponse, error) {
	return nil, nil
}

func newKnowledgeJudge(stub *knowledgeJudgeCompleterStub) knowledgeJudge {
	return knowledgeJudge{completer: stub, model: "qwen-turbo", timeout: constants.KnowledgeJudgeTimeout}
}

func TestKnowledgeJudgeSufficiencyParsesVerdict(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    knowledgeport.SufficiencyVerdict
	}{
		{"sufficient", `{"sufficient": true}`, knowledgeport.SufficiencySufficient},
		{"insufficient", `{"sufficient": false}`, knowledgeport.SufficiencyInsufficient},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &knowledgeJudgeCompleterStub{content: tc.content}
			got, err := newKnowledgeJudge(stub).JudgeSufficiency(context.Background(), "q", "evidence", "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("verdict = %q, want %q", got, tc.want)
			}
			if stub.model != "qwen-turbo" {
				t.Errorf("request model = %q, want qwen-turbo", stub.model)
			}
			user := stub.messages[1].Content
			if !strings.Contains(user, "证据是否足以支撑回答") || !strings.Contains(user, "evidence") {
				t.Errorf("user message must carry sufficiency template, got %q", user)
			}
		})
	}
}

func TestKnowledgeJudgeDegradesOnErrors(t *testing.T) {
	for _, content := range []string{"not json", `{}`} {
		stub := &knowledgeJudgeCompleterStub{content: content}
		if _, err := newKnowledgeJudge(stub).JudgeSufficiency(context.Background(), "q", "e", ""); err == nil {
			t.Errorf("invalid sufficiency response %q must surface an error", content)
		}
	}
	stub := &knowledgeJudgeCompleterStub{content: "not json"}
	if _, err := newKnowledgeJudge(stub).JudgeFaithfulness(context.Background(), "a", "e"); err == nil {
		t.Error("bad JSON must surface an error for faithfulness")
	}
	if _, err := newKnowledgeJudge(stub).JudgeContradiction(context.Background(), "q", "e"); err == nil {
		t.Error("bad JSON must surface an error for contradiction")
	}
	stub.err = context.DeadlineExceeded
	if _, err := newKnowledgeJudge(stub).JudgeSufficiency(context.Background(), "q", "e", ""); err == nil {
		t.Error("completer failure must surface an error")
	}
}

func TestKnowledgeJudgeSufficiencyAppendsInstructions(t *testing.T) {
	stub := &knowledgeJudgeCompleterStub{content: `{"sufficient": true}`}
	jdg := newKnowledgeJudge(stub)

	if _, err := jdg.JudgeSufficiency(context.Background(), "q", "e", ""); err != nil {
		t.Fatal(err)
	}
	if got := stub.messages[1].Content; strings.Contains(got, "附加评分指令") {
		t.Errorf("empty instructions must keep builtin prompt only, got %q", got)
	}

	if _, err := jdg.JudgeSufficiency(context.Background(), "q", "e", "证据不足时禁止猜测"); err != nil {
		t.Fatal(err)
	}
	got := stub.messages[1].Content
	// 指令作为附加段插入，且必须在 JSON 输出结构之前（结构解析安全依赖其不被篡改）。
	idxJSON := strings.Index(got, `"sufficient"`)
	idxIns := strings.Index(got, "证据不足时禁止猜测")
	if idxJSON < 0 {
		t.Errorf("JSON output structure must remain, got %q", got)
	}
	if !strings.Contains(got, "附加评分指令：证据不足时禁止猜测") {
		t.Errorf("instructions must be appended as extra segment, got %q", got)
	}
	if idxIns < 0 || idxIns > idxJSON {
		t.Errorf("instructions (%d) must precede JSON structure (%d), got %q", idxIns, idxJSON, got)
	}
}

func TestKnowledgeJudgeTruncatesEvidence(t *testing.T) {
	long := strings.Repeat("长", constants.KnowledgeJudgeMaxEvidenceRunes*2)
	stub := &knowledgeJudgeCompleterStub{content: `{"sufficient": true}`}
	if _, err := newKnowledgeJudge(stub).JudgeSufficiency(context.Background(), "q", long, ""); err != nil {
		t.Fatal(err)
	}
	got := stub.messages[1].Content
	end := strings.Index(got, "\n\n输出 JSON")
	evidencePart := got[strings.Index(got, "Evidence:\n")+len("Evidence:\n") : end]
	if n := len([]rune(evidencePart)); n != constants.KnowledgeJudgeMaxEvidenceRunes {
		t.Errorf("evidence not truncated: %d runes, want %d", n, constants.KnowledgeJudgeMaxEvidenceRunes)
	}
}

func TestKnowledgeJudgeFaithfulnessParsesClaims(t *testing.T) {
	stub := &knowledgeJudgeCompleterStub{content: `{"claims":[{"text":"a","verdict":"SUPPORTED"},{"text":"b","verdict":"UNSUPPORTED"},{"text":"c","verdict":"WEIRD"}]}`}
	got, err := newKnowledgeJudge(stub).JudgeFaithfulness(context.Background(), "answer", "evidence")
	if err != nil {
		t.Fatal(err)
	}
	want := []knowledgeport.FaithfulnessVerdict{
		knowledgeport.FaithfulnessSupported,
		knowledgeport.FaithfulnessUnsupported,
		knowledgeport.FaithfulnessUnsupported,
	}
	if len(got) != len(want) {
		t.Fatalf("verdict count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("verdict[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestKnowledgeJudgeContradictionParses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    bool
	}{
		{"contradiction", `{"contradiction": true}`, true},
		{"consistent", `{"contradiction": false}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &knowledgeJudgeCompleterStub{content: tc.content}
			got, err := newKnowledgeJudge(stub).JudgeContradiction(context.Background(), "q", "e")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("contradiction = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestKnowledgeJudgeAppliesTimeoutBudget(t *testing.T) {
	j := knowledgeJudge{completer: &knowledgeJudgeCompleterStub{content: `{"sufficient": true}`}, model: "m", timeout: 0}
	ctx, cancel := j.judgeContext(context.Background())
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Error("judge context must carry a deadline")
	}
	j2 := knowledgeJudge{completer: &knowledgeJudgeCompleterStub{}, model: "m", timeout: 5 * time.Second}
	ctx2, cancel2 := j2.judgeContext(context.Background())
	defer cancel2()
	if d, _ := ctx2.Deadline(); d.IsZero() {
		t.Error("explicit timeout must be applied")
	}
}
