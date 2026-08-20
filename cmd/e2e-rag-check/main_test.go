package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	knowledgeapp "github.com/byteBuilderX/stratum/internal/knowledge/application"
	"github.com/stretchr/testify/require"
)

func containsWarningID(t *testing.T, warnings []warning, id string) {
	t.Helper()
	for _, w := range warnings {
		if w.ID == id {
			return
		}
	}
	require.FailNowf(t, "missing warning id", "warnings %v do not contain %s", warnings, id)
}

func notContainsWarningID(t *testing.T, warnings []warning, id string) {
	t.Helper()
	for _, w := range warnings {
		require.NotEqual(t, id, w.ID, "unexpected warning %s", id)
	}
}

// fakeEvaluationRetriever returns a fixed result regardless of the request,
// matching the domain test's fake so evaluateCase runs the real
// EvaluateRetrieval path.
type fakeEvaluationRetriever struct {
	result *knowledgeapp.RAGQueryResult
}

func (f *fakeEvaluationRetriever) Query(context.Context, knowledgeapp.RAGQueryRequest) (*knowledgeapp.RAGQueryResult, error) {
	return f.result, nil
}

func goldenSnap() goldenSnapshot {
	return goldenSnapshot{
		EmbeddingModel: "test-embedding",
		QueryMode:      "hybrid",
		TopK:           metricTopK,
		ChunkSize:      512,
		ChunkOverlap:   64,
		ScoreThreshold: 0,
		Reranking:      knowledgeapp.RerankingNone,
		QueryRewrite:   knowledgeapp.QueryRewriteNone,
	}
}

func TestEvaluateCaseComputesHandCheckedMetrics(t *testing.T) {
	t.Parallel()
	retriever := &fakeEvaluationRetriever{result: &knowledgeapp.RAGQueryResult{
		Sources: []knowledgeapp.Source{
			{DocumentID: "A"}, {DocumentID: "B"}, {DocumentID: "C"},
		},
	}}
	golden := goldenSet{Version: 1, Snapshot: goldenSnap(), Cases: []goldenCase{{
		ID: "c1", Query: "q", Mode: "hybrid", RelevantDocuments: []string{"b.md", "c.md"},
	}}}
	docIDs := map[string]string{"b.md": "B", "c.md": "C"}
	result, err := evaluateCases(context.Background(), retriever, golden, docIDs, "ws", "ws-id")
	require.NoError(t, err)
	require.Len(t, result, 1)
	c := result[0]
	require.True(t, c.Relevant)
	require.False(t, c.NoAnswer)
	require.Equal(t, []string{"A", "B", "C"}, c.RetrievedDocumentIDs)
	require.InDelta(t, 1.0, c.Recall, 1e-6)        // 2/2 relevant
	require.InDelta(t, 2.0/3.0, c.Precision, 1e-6) // 2 of 3 retrieved
	require.InDelta(t, 0.5, c.MRR, 1e-6)           // first relevant at rank 2
	require.InDelta(t, 0.6934, c.NDCG, 1e-3)       // DCG(log2: rank2 .6309 + rank3 .5) / IDCG(1 + .6309)
	require.True(t, c.NoAnswerPass)
}

func TestEvaluateCaseDedupesSameDocumentAcrossChunks(t *testing.T) {
	t.Parallel()
	// One document occupying every rank must count once (B1): recall and NDCG
	// must never exceed 1.
	retriever := &fakeEvaluationRetriever{result: &knowledgeapp.RAGQueryResult{
		Sources: []knowledgeapp.Source{
			{DocumentID: "A"}, {DocumentID: "A"}, {DocumentID: "A"}, {DocumentID: "A"}, {DocumentID: "A"},
		},
	}}
	golden := goldenSet{Version: 1, Snapshot: goldenSnap(), Cases: []goldenCase{{
		ID: "c1", Query: "q", Mode: "hybrid", RelevantDocuments: []string{"a.md"},
	}}}
	result, err := evaluateCases(context.Background(), retriever, golden, map[string]string{"a.md": "A"}, "ws", "ws-id")
	require.NoError(t, err)
	require.Equal(t, []string{"A"}, result[0].RetrievedDocumentIDs)
	require.InDelta(t, 1.0, result[0].Recall, 1e-6)
	require.InDelta(t, 1.0, result[0].NDCG, 1e-6)
	require.InDelta(t, 1.0, result[0].Precision, 1e-6)
}

func TestEvaluateCaseNoAnswerWhenEmptyRetrieval(t *testing.T) {
	t.Parallel()
	retriever := &fakeEvaluationRetriever{result: &knowledgeapp.RAGQueryResult{Sources: []knowledgeapp.Source{}}}
	golden := goldenSet{Version: 1, Snapshot: goldenSnap(), Cases: []goldenCase{{
		ID: "na", Query: "unanswerable", Mode: "hybrid", ExpectNoAnswer: true,
	}}}
	result, err := evaluateCases(context.Background(), retriever, golden, map[string]string{}, "ws", "ws-id")
	require.NoError(t, err)
	require.True(t, result[0].NoAnswer)
	require.True(t, result[0].NoAnswerPass)
	require.Equal(t, 0, result[0].RetrievedCount)
}

func TestBuildCaseWarningsMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		c     caseResult
		want  string
		level string
	}{
		{name: "empty retrieval", c: caseResult{CaseID: "a", RelevantDocuments: []string{"x"}, RetrievedCount: 0}, want: warnEmptyRetrieval, level: warnStrong},
		{name: "recall zero", c: caseResult{CaseID: "b", RelevantDocuments: []string{"x"}, RetrievedCount: 5, Recall: 0}, want: warnRecallZero, level: warnStrong},
		{name: "no answer regression", c: caseResult{CaseID: "c", ExpectNoAnswer: true, NoAnswer: false, RetrievedCount: 3}, want: warnNoAnswerRegression, level: warnWarn},
		{name: "clean", c: caseResult{CaseID: "d", RelevantDocuments: []string{"x"}, RetrievedCount: 3, Recall: 1}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCaseWarnings([]caseResult{tt.c})
			if tt.want == "" {
				require.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			require.Equal(t, tt.want, got[0].ID)
			require.Equal(t, tt.level, got[0].Level)
		})
	}
}

func TestComputeAggregateSeparatesConcerns(t *testing.T) {
	t.Parallel()
	cases := []caseResult{
		{CaseID: "a", ExpectNoAnswer: false, Recall: 1, Precision: 0.5, MRR: 1, NDCG: 0.8, Relevant: true},
		{CaseID: "b", ExpectNoAnswer: false, Recall: 0.5, Precision: 0.25, MRR: 0, NDCG: 0.4, Relevant: false},
		{CaseID: "c", ExpectNoAnswer: true, NoAnswerPass: true},
		{CaseID: "d", ExpectNoAnswer: true, NoAnswerPass: false},
		{CaseID: "e", ExpectNoAnswer: false, Recall: 0, Precision: 0, MRR: 0, NDCG: 0, Relevant: false,
			CitationDocuments: []string{"x"}, CitationCorrect: true},
	}
	agg := computeAggregate(cases)
	require.Equal(t, 5, agg.CaseCount)
	require.Equal(t, 3, agg.NonNoAnswerCount)
	require.Equal(t, 2, agg.NoAnswerCount)
	require.InDelta(t, (1+0.5)/3, agg.Recall, 1e-6)
	require.InDelta(t, (1.0+0+0)/3, agg.MRR, 1e-6)
	require.InDelta(t, 0.5, agg.NoAnswerPassRate, 1e-6)
	require.InDelta(t, 1.0, agg.CitationPassRate, 1e-6) // one annotated case, correct
}

func TestCompareBaselineRegressions(t *testing.T) {
	t.Parallel()
	base := &baseline{
		Provider:  providerFingerprint{EmbeddingModel: "m", ProviderDeclared: true, ProviderBaseURLHash: "h1"},
		Config:    effectiveConfig{EmbeddingModel: "m", TopK: metricTopK},
		Aggregate: aggregate{Recall: 0.9, MRR: 0.8, NDCG: 0.85, NonNoAnswerCount: 3},
	}
	run := providerFingerprint{EmbeddingModel: "m", ProviderDeclared: true, ProviderBaseURLHash: "h1"}
	config := effectiveConfig{EmbeddingModel: "m", TopK: metricTopK}

	t.Run("recall regression only", func(t *testing.T) {
		agg := aggregate{Recall: 0.7, MRR: 0.8, NDCG: 0.85, NonNoAnswerCount: 3}
		warnings, nonComparable := compareBaseline(run, config, agg, base, 0.1)
		require.False(t, nonComparable)
		containsWarningID(t, warnings, warnRecallRegression)
		notContainsWarningID(t, warnings, warnMRRRegression)
		notContainsWarningID(t, warnings, warnNDCGRegression)
	})

	t.Run("no baseline suppresses all", func(t *testing.T) {
		warnings, nonComparable := compareBaseline(run, config, aggregate{}, nil, 0.1)
		require.Empty(t, warnings)
		require.False(t, nonComparable)
	})

	t.Run("embedding drift suppresses deltas", func(t *testing.T) {
		drifted := providerFingerprint{EmbeddingModel: "m2", ProviderDeclared: true, ProviderBaseURLHash: "h1"}
		agg := aggregate{Recall: 0.0, MRR: 0.0, NDCG: 0.0, NonNoAnswerCount: 3}
		warnings, nonComparable := compareBaseline(drifted, config, agg, base, 0.1)
		require.True(t, nonComparable)
		containsWarningID(t, warnings, warnEmbeddingDrift)
		notContainsWarningID(t, warnings, warnRecallRegression)
	})

	t.Run("provider undeclared drifts from declared baseline", func(t *testing.T) {
		undeclared := providerFingerprint{EmbeddingModel: "m", ProviderDeclared: false}
		agg := aggregate{Recall: 0.7, MRR: 0.8, NDCG: 0.85, NonNoAnswerCount: 3}
		warnings, nonComparable := compareBaseline(undeclared, config, agg, base, 0.1)
		require.True(t, nonComparable)
		containsWarningID(t, warnings, warnProviderDrift)
	})
}

func TestRecordBaselineGateFailClosed(t *testing.T) {
	t.Parallel()
	ok := []caseResult{{CaseID: "a", RetrievedCount: 3, Recall: 1}}
	t.Run("requires confirm", func(t *testing.T) {
		err := recordBaselineGate(ok, nil, "p", false)
		require.ErrorContains(t, err, "confirm")
	})
	t.Run("requires provider", func(t *testing.T) {
		err := recordBaselineGate(ok, nil, "", true)
		require.ErrorContains(t, err, "provider")
	})
	t.Run("refuses strong warn", func(t *testing.T) {
		err := recordBaselineGate(ok, []warning{{ID: warnRecallZero, Level: warnStrong}}, "p", true)
		require.ErrorContains(t, err, "strong warnings")
	})
	t.Run("passes clean", func(t *testing.T) {
		require.NoError(t, recordBaselineGate(ok, []warning{{ID: warnNoAnswerRegression, Level: warnWarn}}, "p", true))
	})
}

// TestGoldenDatasetLoads guards the committed golden dataset: it must stay
// loadable by the tool and keep the phase-1 coverage matrix. Dataset edits
// auto-trigger the knowledge-retrieval R3 rule; this test fails closed on an
// invalid or regressed dataset instead of leaving it to the real run.
func TestGoldenDatasetLoads(t *testing.T) {
	t.Parallel()
	dir, err := filepath.Abs("../../test/e2e/knowledge/retrieval/golden")
	require.NoError(t, err)
	set, docs, err := loadGolden(dir)
	require.NoError(t, err)
	require.Len(t, docs, 4, "golden dataset must ship exactly the 4 source documents")

	uniqueRelevant := func(sources []string) int {
		seen := make(map[string]bool, len(sources))
		for _, s := range sources {
			seen[s] = true
		}
		return len(seen)
	}
	var vector, hybrid, keyword, noAnswer, crossDoc, orderingSensitive int
	for _, tc := range set.Cases {
		switch tc.Mode {
		case "vector":
			vector++
		case "keyword":
			keyword++
		case "hybrid":
			hybrid++
		}
		if tc.ExpectNoAnswer {
			noAnswer++
		}
		if n := uniqueRelevant(tc.RelevantDocuments); n >= 2 {
			crossDoc++
			orderingSensitive++ // ≥2 distinct relevant docs is ordering-sensitive by construction
		}
	}
	require.GreaterOrEqual(t, vector, 2)
	require.GreaterOrEqual(t, hybrid, 2)
	require.GreaterOrEqual(t, keyword, 2)
	require.GreaterOrEqual(t, noAnswer, 1)
	require.GreaterOrEqual(t, crossDoc, 1)
	require.GreaterOrEqual(t, orderingSensitive, 2)
}

func TestLoadGoldenValidatesDataset(t *testing.T) {
	t.Parallel()
	writeDataset := func(t *testing.T, cases string, docs map[string]string) string {
		t.Helper()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "cases.yaml"), []byte(cases), 0o600))
		docDir := filepath.Join(dir, "documents")
		require.NoError(t, os.MkdirAll(docDir, 0o755))
		for name, content := range docs {
			require.NoError(t, os.WriteFile(filepath.Join(docDir, name), []byte(content), 0o600))
		}
		return dir
	}
	base := "version: 1\nsnapshot:\n  embedding_model: m\n  query_mode: hybrid\n  top_k: 5\n  chunk_size: 512\n  chunk_overlap: 64\n  score_threshold: 0\n  reranking: \"\"\n  query_rewrite: none\n"

	t.Run("accepts valid dataset", func(t *testing.T) {
		dir := writeDataset(t, base+`
cases:
  - id: c1
    query: q
    mode: hybrid
    relevant_documents: [a.md]
    citation_documents: [a.md]
`, map[string]string{"a.md": "# a\ncontent"})
		_, docs, err := loadGolden(dir)
		require.NoError(t, err)
		require.Len(t, docs, 1)
	})

	t.Run("keyword is a valid per-case mode", func(t *testing.T) {
		dir := writeDataset(t, base+`
cases:
  - id: c1
    query: q
    mode: keyword
    relevant_documents: [a.md]
`, map[string]string{"a.md": "# a\ncontent"})
		_, _, err := loadGolden(dir)
		require.NoError(t, err)
	})

	t.Run("rejects top_k mismatch with RetrievalK", func(t *testing.T) {
		bad := strings.Replace(base, "top_k: 5", "top_k: 3", 1)
		dir := writeDataset(t, bad+`
cases:
  - id: c1
    query: q
    mode: hybrid
    relevant_documents: [a.md]
`, map[string]string{"a.md": "# a\ncontent"})
		_, _, err := loadGolden(dir)
		require.ErrorContains(t, err, "top_k")
	})

	t.Run("rejects non-empty reranking", func(t *testing.T) {
		bad := strings.Replace(base, `reranking: ""`, "reranking: builtin-score-v1", 1)
		dir := writeDataset(t, bad+`
cases:
  - id: c1
    query: q
    mode: hybrid
    relevant_documents: [a.md]
`, map[string]string{"a.md": "# a\ncontent"})
		_, _, err := loadGolden(dir)
		require.ErrorContains(t, err, "reranking")
	})

	t.Run("rejects citation referencing unknown document", func(t *testing.T) {
		dir := writeDataset(t, base+`
cases:
  - id: c1
    query: q
    mode: hybrid
    relevant_documents: [a.md]
    citation_documents: [missing.md]
`, map[string]string{"a.md": "# a\ncontent"})
		_, _, err := loadGolden(dir)
		require.ErrorContains(t, err, "missing.md")
	})

	t.Run("rejects empty relevant for non-no-answer", func(t *testing.T) {
		dir := writeDataset(t, base+`
cases:
  - id: c1
    query: q
    mode: hybrid
`, map[string]string{"a.md": "# a\ncontent"})
		_, _, err := loadGolden(dir)
		require.ErrorContains(t, err, "relevant_documents")
	})
}
