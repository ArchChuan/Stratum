package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	knowledgeapp "github.com/byteBuilderX/stratum/internal/knowledge/application"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/golang-jwt/jwt/v5"
)

// fakeRetriever returns a fixed retrieval result for every query, mirroring
// the retrieval_evaluator_test.go pattern.
type fakeRetriever struct{ docs []string }

func (f *fakeRetriever) Query(ctx context.Context, req knowledgeapp.RAGQueryRequest) (*knowledgeapp.RAGQueryResult, error) {
	sources := make([]knowledgeapp.Source, 0, len(f.docs))
	for i, d := range f.docs {
		sources = append(sources, knowledgeapp.Source{DocumentID: d, Score: float32(0.9) - 0.1*float32(i)})
	}
	return &knowledgeapp.RAGQueryResult{Sources: sources}, nil
}

// goldenSnap returns a golden snapshot that passes validateSnapshot and the
// RetrievalSnapshot.Validate gate used by the evaluator.
func goldenSnap() goldenSnapshot {
	return goldenSnapshot{
		EmbeddingModel: "embedding-3",
		QueryMode:      "hybrid",
		TopK:           5,
		ChunkSize:      512,
		ChunkOverlap:   64,
		ScoreThreshold: 0,
		Reranking:      "",
		QueryRewrite:   "none",
	}
}

func approx(a, b float64) bool {
	d := a - b
	return d > -0.0001 && d < 0.0001
}

func TestAggregateFromKnowledge(t *testing.T) {
	results := []knowledgeCaseResult{
		{CaseID: "a", ExpectNoAnswer: false, Recall: 1, MRR: 1, NDCG: 1, NoAnswerPass: true, CitationCorrect: true, CitationDocuments: []string{"x"}},
		{CaseID: "b", ExpectNoAnswer: false, Recall: 0.5, MRR: 0.5, NDCG: 0.5, NoAnswerPass: true},
	}
	agg := aggregateFromKnowledge(results)
	if agg.CaseCount != 2 || !approx(agg.Recall, 0.75) {
		t.Fatalf("aggregateFromKnowledge = %+v", agg)
	}
	if agg.PassRate <= 0 {
		t.Fatalf("pass_rate should be derived from case outcomes, got %f", agg.PassRate)
	}
	if agg.NonNoAnswerCount != 2 || agg.NoAnswerCount != 0 {
		t.Fatalf("case partition counts wrong: %+v", agg)
	}
	if !approx(agg.CitationPassRate, 1.0) {
		t.Fatalf("citation pass rate over annotated cases = %f", agg.CitationPassRate)
	}
}

func TestAggregateFromKnowledgeSeparatesConcerns(t *testing.T) {
	results := []knowledgeCaseResult{
		{CaseID: "a", ExpectNoAnswer: false, Recall: 1, MRR: 1, NDCG: 1, Relevant: true, NoAnswerPass: true, CitationCorrect: true, CitationDocuments: []string{"x"}},
		{CaseID: "b", ExpectNoAnswer: false, Recall: 0.5, MRR: 0.5, NDCG: 0.5, NoAnswerPass: true},
		{CaseID: "c", ExpectNoAnswer: true, NoAnswer: true, NoAnswerPass: true},
		{CaseID: "d", ExpectNoAnswer: true, NoAnswer: false, NoAnswerPass: false},
	}
	agg := aggregateFromKnowledge(results)
	if agg.CaseCount != 4 || agg.NonNoAnswerCount != 2 || agg.NoAnswerCount != 2 {
		t.Fatalf("case partition counts wrong: %+v", agg)
	}
	if !approx(agg.Recall, 0.75) || !approx(agg.MRR, 0.75) || !approx(agg.NDCG, 0.75) {
		t.Fatalf("ordering metric averages wrong: %+v", agg)
	}
	if !approx(agg.RelevantRate, 0.5) {
		t.Fatalf("relevant rate = %f, want 0.5", agg.RelevantRate)
	}
	if !approx(agg.NoAnswerPassRate, 0.5) {
		t.Fatalf("no-answer pass rate = %f, want 0.5", agg.NoAnswerPassRate)
	}
	if !approx(agg.CitationPassRate, 1.0) {
		t.Fatalf("citation pass rate = %f, want 1.0 (one annotated, correct)", agg.CitationPassRate)
	}
	if !approx(agg.PassRate, 0.75) {
		t.Fatalf("pass rate = %f, want 0.75 (a,b,c pass, d fails)", agg.PassRate)
	}
}

func TestEvaluateCaseComputesHandCheckedMetrics(t *testing.T) {
	ctx := context.Background()
	evaluator := knowledgeapp.NewRetrievalEvaluator(&fakeRetriever{docs: []string{"A", "B", "C"}})
	result, err := evaluateCase(ctx, evaluator, goldenCase{
		ID: "case-1", Query: "where is the billing page?", Mode: "vector",
		RelevantDocuments: []string{"b.md"},
	}, goldenSnap(), map[string]string{"b.md": "B"}, "eval-check-test", "ws-id")
	if err != nil {
		t.Fatalf("evaluateCase: %v", err)
	}
	if result.CaseID != "case-1" || result.RetrievedCount != 3 {
		t.Fatalf("case id/count wrong: %+v", result)
	}
	// Relevant doc B sits at rank 2: recall 1.0 (B in top-k), MRR 0.5,
	// nDCG = (1/log2(3)) / (1/log2(2)) = 0.6309.
	if !approx(result.Recall, 1.0) || !approx(result.MRR, 0.5) || !approx(result.NDCG, 1/math.Log2(3)) {
		t.Fatalf("ordering metrics wrong: recall=%f mrr=%f ndcg=%f", result.Recall, result.MRR, result.NDCG)
	}
	if !result.Relevant || !result.CitationCorrect || result.NoAnswer {
		t.Fatalf("evaluation flags wrong: %+v", result)
	}
	if !result.NoAnswerPass {
		t.Fatalf("answerable case should pass no-answer gate: %+v", result)
	}
}

func TestEvaluateCaseDedupesSameDocumentAcrossChunks(t *testing.T) {
	ctx := context.Background()
	// Five chunks all belong to the same document id: document-level metrics
	// must count the first occurrence only.
	evaluator := knowledgeapp.NewRetrievalEvaluator(&fakeRetriever{docs: []string{"A", "A", "A", "A", "A"}})
	result, err := evaluateCase(ctx, evaluator, goldenCase{
		ID: "dedupe", Query: "onboarding", Mode: "vector",
		RelevantDocuments: []string{"a.md"},
	}, goldenSnap(), map[string]string{"a.md": "A"}, "eval-check-test", "ws-id")
	if err != nil {
		t.Fatalf("evaluateCase: %v", err)
	}
	if result.RetrievedCount != 1 {
		t.Fatalf("retrieved count = %d, want 1 after dedupe", result.RetrievedCount)
	}
	if !approx(result.Recall, 1.0) || !approx(result.MRR, 1.0) || !approx(result.NDCG, 1.0) {
		t.Fatalf("ordering metrics wrong after dedupe: %+v", result)
	}
}

func TestEvaluateCaseNoAnswerWhenEmptyRetrieval(t *testing.T) {
	ctx := context.Background()
	evaluator := knowledgeapp.NewRetrievalEvaluator(&fakeRetriever{docs: nil})
	result, err := evaluateCase(ctx, evaluator, goldenCase{
		ID: "na-1", Query: "offline store membership points", Mode: "keyword",
		RelevantDocuments: []string{}, ExpectNoAnswer: true,
	}, goldenSnap(), map[string]string{}, "eval-check-test", "ws-id")
	if err != nil {
		t.Fatalf("evaluateCase: %v", err)
	}
	if !result.NoAnswer || result.RetrievedCount != 0 {
		t.Fatalf("expected empty retrieval to trigger no-answer: %+v", result)
	}
	if !result.NoAnswerPass {
		t.Fatalf("expect_no_answer case with empty retrieval must pass: %+v", result)
	}
	if !casePassed(result) {
		t.Fatalf("case with no citation should pass when no-answer gate passes: %+v", result)
	}
}

func TestBuildCaseWarningsMatrix(t *testing.T) {
	cases := []knowledgeCaseResult{
		{CaseID: "empty", ExpectNoAnswer: false, RetrievedCount: 0, RelevantDocuments: []string{"a.md"}},
		{CaseID: "zero", ExpectNoAnswer: false, RetrievedCount: 3, Recall: 0, RelevantDocuments: []string{"a.md"}},
		{CaseID: "noans", ExpectNoAnswer: true, NoAnswer: false, RetrievedCount: 2},
		{CaseID: "ok", ExpectNoAnswer: false, RetrievedCount: 2, Recall: 1, NoAnswerPass: true},
	}
	warnings := buildCaseWarnings(cases)
	if len(warnings) != 3 {
		t.Fatalf("expected 3 warnings, got %d: %+v", len(warnings), warnings)
	}
	byCase := map[string]string{}
	for _, w := range warnings {
		byCase[w.CaseID] = w.ID
	}
	if byCase["empty"] != warnEmptyRetrieval || byCase["zero"] != warnRecallZero || byCase["noans"] != warnNoAnswerRegression {
		t.Fatalf("warning ids wrong: %+v", byCase)
	}
	if warnings[0].Level != warnStrong || warnings[1].Level != warnStrong || warnings[2].Level != warnWarn {
		t.Fatalf("warning levels wrong: %+v", warnings)
	}
}

func TestOutcomesFromKnowledge(t *testing.T) {
	results := []knowledgeCaseResult{
		{CaseID: "a", ExpectNoAnswer: false, Recall: 1, MRR: 1, NDCG: 1, NoAnswerPass: true, RetrievedCount: 2, RetrievedDocumentIDs: []string{"A", "B"}},
	}
	out := outcomesFromKnowledge(results)
	if len(out) != 1 {
		t.Fatalf("outcomesFromKnowledge length = %d", len(out))
	}
	o := out[0]
	if o.CaseID != "a" || o.AssertionMode != "retrieval" || !o.Passed {
		t.Fatalf("case outcome mapping wrong: %+v", o)
	}
	if o.RetrievedCount != 2 || len(o.RetrievedDocIDs) != 2 {
		t.Fatalf("retrieval fields wrong: %+v", o)
	}
	if o.NoAnswerPass == nil || *o.NoAnswerPass != true {
		t.Fatalf("NoAnswerPass pointer missing: %+v", o)
	}
	if o.CitationCorrect == nil || *o.CitationCorrect != false {
		t.Fatalf("CitationCorrect pointer missing: %+v", o)
	}
}

func TestNewWorkspaceName(t *testing.T) {
	o := options{point: "retrieval"}
	if got := newWorkspaceName(o, point{}); got != "eval-check-retrieval" {
		t.Fatalf("newWorkspaceName = %q", got)
	}
}

func TestKnowledgeFingerprint(t *testing.T) {
	snap := map[string]any{
		"embedding_model": "embedding-3", "query_mode": "hybrid", "top_k": 5,
		"chunk_size": 512, "chunk_overlap": 64, "score_threshold": 0.0,
		"reranking": "", "query_rewrite": "none",
	}
	fp := knowledgeFingerprint(snap, "provider-x")
	if fp.Hash == "" || fp.ProviderHash == "" {
		t.Fatalf("fingerprint fields empty: %+v", fp)
	}
	fpOther := knowledgeFingerprint(snap, "provider-y")
	if fp.Hash == fpOther.Hash {
		t.Fatalf("provider change should change the fingerprint hash")
	}
	fpConfig := knowledgeFingerprint(map[string]any{
		"embedding_model": "embedding-3", "query_mode": "hybrid", "top_k": 10,
		"chunk_size": 512, "chunk_overlap": 64, "score_threshold": 0.0,
		"reranking": "", "query_rewrite": "none",
	}, "provider-x")
	if fp.Hash == fpConfig.Hash {
		t.Fatalf("config change should change the fingerprint hash")
	}
}

func TestFingerprintOfPointDispatchesKnowledge(t *testing.T) {
	o := options{provider: "provider-x"}
	p := point{Kind: "knowledge", Snapshot: map[string]any{
		"embedding_model": "embedding-3", "query_mode": "hybrid", "top_k": 5,
		"chunk_size": 512, "chunk_overlap": 64, "score_threshold": 0.0,
		"reranking": "", "query_rewrite": "none",
	}}
	fp := fingerprintOfPoint(o, p)
	if fp.Hash == "" || fp.Hash == "todo" {
		t.Fatalf("knowledge fingerprint not applied: %+v", fp)
	}
}

func TestGoldenDatasetLoads(t *testing.T) {
	dir, err := filepath.Abs("../../test/e2e/knowledge/retrieval/golden")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	set, documents, err := loadGolden(dir)
	if err != nil {
		t.Fatalf("loadGolden: %v", err)
	}
	if set.Version != 1 || len(set.Cases) == 0 {
		t.Fatalf("dataset header wrong: %+v", set)
	}
	if len(documents) != 4 {
		t.Fatalf("expected 4 golden documents, got %d", len(documents))
	}
}

func TestLoadGoldenValidatesDataset(t *testing.T) {
	valid := `version: 1
snapshot:
  embedding_model: embedding-3
  query_mode: hybrid
  top_k: 5
  chunk_size: 512
  chunk_overlap: 64
  score_threshold: 0
  reranking: ""
  query_rewrite: none
cases:
  - id: c1
    query: how do I reset my password
    mode: vector
    relevant_documents: [faq.md]
`
	dir, err := writeGoldenDataset(t, valid, map[string]string{"faq.md": "# FAQ\n"})
	if err != nil {
		t.Fatalf("write golden: %v", err)
	}
	if _, _, err := loadGolden(dir); err != nil {
		t.Fatalf("valid dataset rejected: %v", err)
	}

	tests := []struct {
		name string
		yaml string
		docs map[string]string
	}{
		{"unsupported version", strings.Replace(valid, "version: 1", "version: 2", 1), map[string]string{"faq.md": "x"}},
		{"empty cases", strings.Replace(valid, "  - id: c1\n    query: how do I reset my password\n    mode: vector\n    relevant_documents: [faq.md]\n", "", 1), map[string]string{"faq.md": "x"}},
		{"top_k mismatch", strings.Replace(valid, "top_k: 5", "top_k: 3", 1), map[string]string{"faq.md": "x"}},
		{"unknown reference", strings.Replace(valid, "relevant_documents: [faq.md]", "relevant_documents: [missing.md]", 1), map[string]string{"faq.md": "x"}},
		{"bad query mode", strings.Replace(valid, "query_mode: hybrid", "query_mode: fuzzy", 1), map[string]string{"faq.md": "x"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir, err := writeGoldenDataset(t, tc.yaml, tc.docs)
			if err != nil {
				t.Fatalf("write golden: %v", err)
			}
			if _, _, err := loadGolden(dir); err == nil {
				t.Fatalf("invalid dataset %q accepted", tc.name)
			}
		})
	}
}

func writeGoldenDataset(t *testing.T, casesYAML string, docs map[string]string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cases.yaml"), []byte(casesYAML), 0o600); err != nil {
		return "", err
	}
	docDir := filepath.Join(dir, "documents")
	if err := os.MkdirAll(docDir, 0o700); err != nil {
		return "", err
	}
	for name, content := range docs {
		if err := os.WriteFile(filepath.Join(docDir, name), []byte(content), 0o600); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func TestClassifyHTTP(t *testing.T) {
	if err := classifyHTTP("/health", http.StatusOK, "ok"); err != nil {
		t.Fatalf("2xx should not error: %v", err)
	}
	if err := classifyHTTP("/x", http.StatusBadRequest, "bad"); err == nil {
		t.Fatalf("4xx should be a defect error")
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError, http.StatusBadGateway} {
		err := classifyHTTP("/x", status, "fail")
		if err == nil {
			t.Fatalf("status %d should error", status)
		}
		if !isInfra(err) {
			t.Fatalf("status %d should be infra", status)
		}
	}
}

func TestIndexDocuments(t *testing.T) {
	docs := []document{
		{Source: "a.md", ID: "doc-1", IngestStatus: constants.IngestStatusCompleted, ProcessedChunks: 3},
		{Source: "b.md", ID: "doc-2", IngestStatus: constants.IngestStatusProcessing},
		{Source: "c.md", ID: "doc-3", IngestStatus: constants.IngestStatusCompleted, ProcessedChunks: 1},
	}
	index, err := indexDocuments(docs)
	if err != nil {
		t.Fatalf("indexDocuments: %v", err)
	}
	if index["a.md"] != "doc-1" || index["c.md"] != "doc-3" {
		t.Fatalf("index wrong: %+v", index)
	}
	if _, ok := index["b.md"]; ok {
		t.Fatalf("processing document must not be indexed: %+v", index)
	}
	dups := []document{
		{Source: "a.md", ID: "doc-1", IngestStatus: constants.IngestStatusCompleted},
		{Source: "a.md", ID: "doc-2", IngestStatus: constants.IngestStatusCompleted},
	}
	if _, err := indexDocuments(dups); err == nil {
		t.Fatalf("duplicate source should error")
	}
}

func TestClassifyIngest(t *testing.T) {
	docs := []document{
		{Source: "a.md", IngestStatus: constants.IngestStatusCompleted, ProcessedChunks: 2},
		{Source: "b.md", IngestStatus: constants.IngestStatusProcessing},
	}
	state := classifyIngest(docs, []string{"a.md", "b.md", "c.md"})
	if !state.pending || state.allReady {
		t.Fatalf("pending state wrong: %+v", state)
	}
	if len(state.missing) != 1 || state.missing[0] != "c.md" {
		t.Fatalf("missing sources wrong: %+v", state.missing)
	}
	failed := []document{
		{Source: "a.md", IngestStatus: constants.IngestStatusCompleted, ProcessedChunks: 0},
		{Source: "b.md", IngestStatus: constants.IngestStatusFailed, IngestError: "boom"},
	}
	state = classifyIngest(failed, []string{"a.md", "b.md"})
	if len(state.failed) != 2 {
		t.Fatalf("expected both failure modes, got %+v", state.failed)
	}
}

func TestHTTPRetrieverQueryWithMockServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/knowledge/query" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sources":[{"document_id":"A","content":"x","score":0.9}],"best_score":0.9}`))
	}))
	defer server.Close()

	client := newHTTPClient(server.URL, "token")
	retriever := &httpEvaluationRetriever{client: client}
	result, err := retriever.Query(context.Background(), knowledgeapp.RAGQueryRequest{
		Question: "q", Workspace: "ws", Mode: "vector", TopK: 5,
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(result.Sources) != 1 || result.Sources[0].DocumentID != "A" || result.Sources[0].Score != 0.9 {
		t.Fatalf("query result wrong: %+v", result.Sources)
	}
}

func TestMintOwnerTokenJTI(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	t.Setenv("JWT_PRIVATE_KEY_PEM", string(pemBytes))

	token, err := mintOwnerToken("tenant-1", "user-1")
	if err != nil {
		t.Fatalf("mintOwnerToken: %v", err)
	}
	parsed, err := jwt.Parse(token, func(tok *jwt.Token) (any, error) {
		if tok.Method != jwt.SigningMethodRS256 {
			return nil, fmt.Errorf("unexpected signing method %v", tok.Method)
		}
		return &key.PublicKey, nil
	})
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("unexpected claims type %T", parsed.Claims)
	}
	if jti, _ := claims["jti"].(string); !strings.HasPrefix(jti, "eval-check-") {
		t.Fatalf("jti %q does not carry eval-check- prefix", jti)
	}
	if claims["tid"] != "tenant-1" || claims["sub"] != "user-1" {
		t.Fatalf("claims wrong: %+v", claims)
	}
}
