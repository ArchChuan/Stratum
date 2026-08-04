package rerank

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"go.uber.org/zap"
)

// fakeDoer records the last request and returns a canned response.
type fakeDoer struct {
	req        *http.Request
	body       []byte
	statusCode int
	payload    string
	err        error
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.req = req
	f.body, _ = io.ReadAll(req.Body)
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{
		StatusCode: f.statusCode,
		Body:       io.NopCloser(strings.NewReader(f.payload)),
	}, nil
}

type recordingMetrics struct {
	observability.NoopMetrics
	requests []string // tenant:model:status
}

func (r *recordingMetrics) IncRerankRequest(tenantID, model, status string) {
	r.requests = append(r.requests, tenantID+":"+model+":"+status)
}

func TestCohereRerankerSendsAuthenticatedRequest(t *testing.T) {
	doer := &fakeDoer{statusCode: http.StatusOK,
		payload: `{"results":[{"index":2,"relevance_score":0.91},{"index":0,"relevance_score":0.45}]}`}
	reranker := NewCohereReranker("https://api.cohere.example/", "secret-key", "rerank-v3.0",
		doer, &recordingMetrics{}, zap.NewNop())

	results, err := reranker.Rerank(reqctx.WithTenantID(context.Background(), "tenant-1"),
		port.RerankRequest{Query: "refund policy", Documents: []string{"doc a", "doc b", "doc c"}, TopN: 2})
	if err != nil {
		t.Fatal(err)
	}
	if doer.req.URL.String() != "https://api.cohere.example/v2/rerank" {
		t.Fatalf("unexpected URL %q", doer.req.URL.String())
	}
	if got := doer.req.Header.Get("Authorization"); got != "Bearer secret-key" {
		t.Fatalf("unexpected authorization header %q", got)
	}
	var sent cohereRerankRequest
	if err := json.Unmarshal(doer.body, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Model != "rerank-v3.0" || sent.Query != "refund policy" ||
		len(sent.Documents) != 3 || sent.TopN != 2 {
		t.Fatalf("unexpected request body: %+v", sent)
	}
	if len(results) != 2 || results[0].Index != 2 || results[0].Score != 0.91 ||
		results[1].Index != 0 || results[1].Score != 0.45 {
		t.Fatalf("unexpected results: %+v", results)
	}
}

func TestCohereRerankerReportsErrorStatus(t *testing.T) {
	metrics := &recordingMetrics{}
	reranker := NewCohereReranker("https://api.cohere.example", "key", "rerank-v3.0",
		&fakeDoer{statusCode: http.StatusUnauthorized, payload: `{"message":"bad key"}`},
		metrics, zap.NewNop())

	if _, err := reranker.Rerank(reqctx.WithTenantID(context.Background(), "tenant-1"),
		port.RerankRequest{Query: "q", Documents: []string{"a"}, TopN: 1}); err == nil ||
		!strings.Contains(err.Error(), "401") {
		t.Fatalf("expected status error, got %v", err)
	}
	want := []string{"tenant-1:rerank-v3.0:error"}
	if len(metrics.requests) != 1 || metrics.requests[0] != want[0] {
		t.Fatalf("metrics=%v want %v", metrics.requests, want)
	}
}

func TestCohereRerankerMapsTransportErrorToErrorMetric(t *testing.T) {
	metrics := &recordingMetrics{}
	reranker := NewCohereReranker("https://api.cohere.example", "key", "rerank-v3.0",
		&fakeDoer{err: errors.New("connection refused")}, metrics, zap.NewNop())

	if _, err := reranker.Rerank(context.Background(),
		port.RerankRequest{Query: "q", Documents: []string{"a"}, TopN: 1}); err == nil ||
		!strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("expected transport error, got %v", err)
	}
	if got := metrics.requests[0]; got != ":rerank-v3.0:error" {
		t.Fatalf("tenant-less context must still record model/status, got %q", got)
	}
}

func TestCohereRerankerRecordsSuccessMetric(t *testing.T) {
	metrics := &recordingMetrics{}
	reranker := NewCohereReranker("https://api.cohere.example", "key", "rerank-v3.0",
		&fakeDoer{statusCode: http.StatusOK, payload: `{"results":[]}`}, metrics, zap.NewNop())

	if _, err := reranker.Rerank(reqctx.WithTenantID(context.Background(), "tenant-9"),
		port.RerankRequest{Query: "q", Documents: []string{"a"}, TopN: 1}); err != nil {
		t.Fatal(err)
	}
	if got := metrics.requests[0]; got != "tenant-9:rerank-v3.0:ok" {
		t.Fatalf("metrics=%q", got)
	}
}
