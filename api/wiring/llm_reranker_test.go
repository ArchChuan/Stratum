package wiring

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"go.uber.org/zap"
)

// llmRerankerCompleterStub captures the completion request for assertions.
type llmRerankerCompleterStub struct {
	model    string
	messages []llmgatewaydomain.Message
	temp     *float64
	maxTok   int
	format   *llmgatewaydomain.ResponseFormat
	content  string
	err      error
}

func (s *llmRerankerCompleterStub) Complete(_ context.Context, req *llmgatewaydomain.CompletionRequest) (*llmgatewaydomain.CompletionResponse, error) {
	s.model = req.Model
	s.messages = req.Messages
	s.temp = req.Temperature
	s.maxTok = req.MaxTokens
	s.format = req.ResponseFormat
	if s.err != nil {
		return nil, s.err
	}
	return &llmgatewaydomain.CompletionResponse{Content: s.content}, nil
}

func (s *llmRerankerCompleterStub) CompleteStream(context.Context, *llmgatewaydomain.CompletionRequest, func(string)) (*llmgatewaydomain.CompletionResponse, error) {
	return nil, nil
}

// blockingCompleter blocks until ctx cancellation (timeout propagation test).
type blockingCompleter struct{}

func (blockingCompleter) Complete(ctx context.Context, _ *llmgatewaydomain.CompletionRequest) (*llmgatewaydomain.CompletionResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockingCompleter) CompleteStream(context.Context, *llmgatewaydomain.CompletionRequest, func(string)) (*llmgatewaydomain.CompletionResponse, error) {
	return nil, nil
}

// rerankMetricRecorder records the two rerank metrics alongside NoopMetrics so
// the fixed "builtin-llm" label is asserted.
type rerankMetricRecorder struct {
	observability.NoopMetrics
	inc []string // tenant:model:status
	dur []float64
}

func (m *rerankMetricRecorder) IncRerankRequest(tenantID, model, status string) {
	m.inc = append(m.inc, tenantID+":"+model+":"+status)
}

func (m *rerankMetricRecorder) RecordRerankDuration(model string, seconds float64) {
	m.dur = append(m.dur, seconds)
}

func newLLMRerankerStub(stub *llmRerankerCompleterStub, metrics observability.MetricsProvider) *llmReranker {
	return newLLMReranker(stub, "qwen-turbo", constants.RerankLLMTimeout, metrics, zap.NewNop())
}

func TestLLMRerankerParsesScoresAndConfiguresDeterministicCall(t *testing.T) {
	stub := &llmRerankerCompleterStub{content: `{"scores":[{"index":1,"score":0.9},{"index":0,"score":0.4}]}`}
	got, err := newLLMRerankerStub(stub, nil).Rerank(context.Background(), knowledgeport.RerankRequest{
		Query: "q", Documents: []string{"a", "b"}, TopN: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Index != 1 || got[0].Score != 0.9 || got[1].Index != 0 {
		t.Fatalf("results=%+v", got)
	}
	if stub.model != "qwen-turbo" || stub.temp == nil || *stub.temp != 0 {
		t.Fatalf("model=%q temp=%v want deterministic 0", stub.model, stub.temp)
	}
	if stub.maxTok != constants.RerankLLMMaxTokens || stub.format == nil || stub.format.Type != "json_object" {
		t.Fatalf("maxTokens=%d format=%+v", stub.maxTok, stub.format)
	}
	user := stub.messages[1].Content
	if !strings.Contains(user, "Query:\nq") || !strings.Contains(user, "0. a") {
		t.Fatalf("prompt must carry query and numbered candidates, got %q", user)
	}
}

func TestLLMRerankerTruncatesCandidates(t *testing.T) {
	long := strings.Repeat("长", constants.RerankLLMMaxDocRunes*2)
	stub := &llmRerankerCompleterStub{content: `{"scores":[]}`}
	if _, err := newLLMRerankerStub(stub, nil).Rerank(context.Background(), knowledgeport.RerankRequest{
		Query: "q", Documents: []string{long}, TopN: 1,
	}); err != nil {
		t.Fatal(err)
	}
	user := stub.messages[1].Content
	candidate := user[strings.Index(user, "0. ")+3 : strings.Index(user, "\n\n输出 JSON")]
	if n := len([]rune(candidate)); n != constants.RerankLLMMaxDocRunes {
		t.Fatalf("candidate truncated to %d runes, want %d", n, constants.RerankLLMMaxDocRunes)
	}
}

func TestLLMRerankerDedupsAndSkipsInvalidIndex(t *testing.T) {
	stub := &llmRerankerCompleterStub{content: `{"scores":[{"index":0,"score":0.9},{"index":0,"score":0.1},{"index":99,"score":0.8},{"index":-1,"score":0.7}]}`}
	got, err := newLLMRerankerStub(stub, nil).Rerank(context.Background(), knowledgeport.RerankRequest{
		Query: "q", Documents: []string{"a", "b"}, TopN: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Index != 0 || got[0].Score != 0.9 {
		t.Fatalf("duplicate must keep first occurrence, invalid indexes skipped: %+v", got)
	}
}

func TestLLMRerankerSurfacesErrors(t *testing.T) {
	for name, stub := range map[string]*llmRerankerCompleterStub{
		"completer error": {err: errors.New("upstream down")},
		"bad json":        {content: "not json"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newLLMRerankerStub(stub, nil).Rerank(context.Background(), knowledgeport.RerankRequest{
				Query: "q", Documents: []string{"a", "b"},
			}); err == nil {
				t.Fatal("must surface an error")
			}
		})
	}
}

func TestLLMRerankerEmptyScoresIsEmptyResult(t *testing.T) {
	stub := &llmRerankerCompleterStub{content: `{"scores":[]}`}
	got, err := newLLMRerankerStub(stub, nil).Rerank(context.Background(), knowledgeport.RerankRequest{
		Query: "q", Documents: []string{"a", "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("empty scores must yield empty results, got %+v", got)
	}
}

func TestLLMRerankerNilMetricsTolerated(t *testing.T) {
	stub := &llmRerankerCompleterStub{content: `{"scores":[]}`}
	if _, err := newLLMReranker(stub, "m", 0, nil, zap.NewNop()).Rerank(context.Background(), knowledgeport.RerankRequest{
		Query: "q", Documents: []string{"a"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLLMRerankerRecordsMetrics(t *testing.T) {
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")

	m := &rerankMetricRecorder{}
	stub := &llmRerankerCompleterStub{content: `{"scores":[{"index":0,"score":1}]}`}
	if _, err := newLLMRerankerStub(stub, m).Rerank(ctx, knowledgeport.RerankRequest{Query: "q", Documents: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if len(m.inc) != 1 || m.inc[0] != "tenant-1:builtin-llm:ok" {
		t.Fatalf("metrics=%v", m.inc)
	}
	if len(m.dur) != 1 {
		t.Fatalf("duration not recorded: %v", m.dur)
	}

	m2 := &rerankMetricRecorder{}
	failStub := &llmRerankerCompleterStub{err: errors.New("boom")}
	if _, err := newLLMRerankerStub(failStub, m2).Rerank(ctx, knowledgeport.RerankRequest{Query: "q", Documents: []string{"a"}}); err == nil {
		t.Fatal("must fail")
	}
	if len(m2.inc) != 1 || m2.inc[0] != "tenant-1:builtin-llm:error" {
		t.Fatalf("metrics=%v", m2.inc)
	}
}

func TestLLMRerankerAppliesTimeoutBudget(t *testing.T) {
	r := &llmReranker{timeout: 0}
	if d := r.rerankTimeout(); d != constants.RerankLLMTimeout {
		t.Fatalf("zero timeout must fall back to %v, got %v", constants.RerankLLMTimeout, d)
	}
	r2 := &llmReranker{timeout: 7 * time.Second}
	if d := r2.rerankTimeout(); d != 7*time.Second {
		t.Fatalf("explicit timeout must be kept, got %v", d)
	}
}

func TestLLMRerankerTimeoutCancelsBlockedCompleter(t *testing.T) {
	r := newLLMReranker(blockingCompleter{}, "m", 20*time.Millisecond, nil, zap.NewNop())
	_, err := r.Rerank(context.Background(), knowledgeport.RerankRequest{Query: "q", Documents: []string{"a"}})
	if err == nil {
		t.Fatal("blocked completer must be cancelled by the timeout")
	}
}
