package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	evalapp "github.com/byteBuilderX/stratum/internal/evaluation/application"
	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	evalport "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	knowledgeapp "github.com/byteBuilderX/stratum/internal/knowledge/application"
	knowledgedomain "github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

func TestKnowledgeEvaluationAdapterCreatesPublishedImmutableBaseline(t *testing.T) {
	revisions := &fakeKnowledgeRevisionService{}
	source := &fakeKnowledgeSnapshotSource{workspace: &knowledgedomain.Workspace{ID: "workspace-id", Name: "support",
		Config: knowledgedomain.WorkspaceConfig{EmbeddingModel: "embedding-3", QueryMode: "hybrid", TopK: 7}},
		documents: []*knowledgedomain.Document{
			{ID: "doc-b", ContentHash: "hash-b", IngestStatus: "completed"},
			{ID: "doc-a", ContentHash: "hash-a", IngestStatus: "completed"},
		}}
	adapter := knowledgeEvaluationAdapter{revisions: revisions, source: source, actorID: "evaluation-worker"}
	ref, err := adapter.CreatePublishedBaseline(context.Background(), "tenant-1", "support")
	if err != nil || ref.Kind != evaldomain.ResourceKindKnowledge || revisions.publishCalls != 1 {
		t.Fatalf("ref=%+v publish=%d err=%v", ref, revisions.publishCalls, err)
	}
	snapshot := revisions.input.Payload.(knowledgeRetrievalSnapshot)
	if snapshot.WorkspaceID != "workspace-id" || snapshot.DocumentSetHash == "" ||
		snapshot.EmbeddingIdentity != "embedding-3" || snapshot.TopK != 7 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	encoded, _ := json.Marshal(revisions.input.SafeSummary)
	if strings.Contains(string(encoded), "document") || strings.Contains(string(encoded), "embedding") {
		t.Fatalf("unsafe summary: %s", encoded)
	}
	if source.tenantID != "tenant-1" {
		t.Fatalf("tenant not propagated: %q", source.tenantID)
	}
}

func TestKnowledgeEvaluationAdapterCandidateIsBoundedIdempotentAndExact(t *testing.T) {
	snapshot := knowledgeRetrievalSnapshot{TenantID: "tenant-1", WorkspaceID: "workspace-id", WorkspaceName: "support",
		DocumentSetHash: "documents", EmbeddingIdentity: "embedding-3", QueryMode: "hybrid", TopK: 5,
		ScoreThreshold: 0.2, RerankingIdentity: knowledgeRerankingIdentity, Reranking: "none", QueryRewrite: "none"}
	revisions := publishedKnowledgeRevisions(t, snapshot)
	adapter := knowledgeEvaluationAdapter{revisions: revisions, evaluator: knowledgeapp.NewRetrievalEvaluator(
		&fakeKnowledgeRetriever{result: &knowledgeapp.RAGQueryResult{}})}
	patch := evaldomain.CandidatePatch{Source: "bounded", ParameterPatch: map[string]any{
		"top_k": float64(8), "score_threshold": 0.4, "reranking": "score_desc", "query_rewrite": "lowercase_trim",
	}}
	first, err := adapter.CreateCandidate(context.Background(), "tenant-1", knowledgeRef("published-1"), patch)
	if err != nil {
		t.Fatal(err)
	}
	second, err := adapter.CreateCandidate(context.Background(), "tenant-1", knowledgeRef("published-1"), patch)
	if err != nil || first != second || !strings.HasPrefix(revisions.input.IdempotencyKey, "knowledge-candidate-") {
		t.Fatalf("first=%+v second=%+v key=%q err=%v", first, second, revisions.input.IdempotencyKey, err)
	}
	candidate := revisions.input.Payload.(knowledgeRetrievalSnapshot)
	if candidate.WorkspaceID != snapshot.WorkspaceID || candidate.DocumentSetHash != snapshot.DocumentSetHash ||
		candidate.EmbeddingIdentity != snapshot.EmbeddingIdentity ||
		candidate.RerankingIdentity != snapshot.RerankingIdentity || candidate.TopK != 8 {
		t.Fatalf("immutable state changed: %+v", candidate)
	}
	for _, unsafe := range []map[string]any{{"workspace_id": "other"}, {"tenant_id": "other"},
		{"embedding_identity": "other"}, {"documents": []any{}}, {"top_k": float64(101)}, {"query_rewrite": "llm"}} {
		if _, err := adapter.CreateCandidate(context.Background(), "tenant-1", knowledgeRef("published-1"),
			evaldomain.CandidatePatch{ParameterPatch: unsafe}); err == nil {
			t.Fatalf("accepted unsafe patch: %#v", unsafe)
		}
	}
}

func TestKnowledgeEvaluationAdapterRequiresPublishedTenantRevisionAndSafeOutput(t *testing.T) {
	documents := []*knowledgedomain.Document{{ID: "doc-1", ContentHash: "content-hash", IngestStatus: "completed"}}
	documentSetHash, err := knowledgeDocumentSetHash(documents)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := knowledgeRetrievalSnapshot{TenantID: "tenant-1", WorkspaceID: "workspace-id", WorkspaceName: "support",
		DocumentSetHash: documentSetHash, EmbeddingIdentity: "embedding-3", QueryMode: "vector", TopK: 5,
		RerankingIdentity: knowledgeRerankingIdentity, Reranking: "none", QueryRewrite: "none"}
	retriever := &fakeKnowledgeRetriever{result: &knowledgeapp.RAGQueryResult{Sources: []knowledgeapp.Source{
		{DocumentID: "doc-1", Content: "raw secret document", Score: 0.9},
	}}}
	adapter := knowledgeEvaluationAdapter{revisions: publishedKnowledgeRevisions(t, snapshot),
		evaluator: knowledgeapp.NewRetrievalEvaluator(retriever),
		source:    &fakeKnowledgeSnapshotSource{documents: documents}}
	result, err := adapter.ExecuteRevision(context.Background(), "tenant-1", "user-1", knowledgeRef("published-1"), evaldomain.EvalCase{
		Input: map[string]any{"query": "refund", "relevant_document_ids": []any{"doc-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result.Output)
	if strings.Contains(string(encoded), "raw secret") || retriever.request.WorkspaceID != "workspace-id" {
		t.Fatalf("output=%s request=%+v", encoded, retriever.request)
	}

	draft := publishedKnowledgeRevisions(t, snapshot)
	draft.revision.Status = evaldomain.RevisionStatusDraft
	if _, err := (knowledgeEvaluationAdapter{revisions: draft}).ResolveRevision(
		context.Background(), "tenant-1", knowledgeRef("published-1")); !errors.Is(err, evaldomain.ErrRevisionNotPublished) {
		t.Fatalf("expected draft rejection, got %v", err)
	}
	if _, err := (knowledgeEvaluationAdapter{revisions: &fakeKnowledgeRevisionService{found: false}}).ResolveRevision(
		context.Background(), "other-tenant", knowledgeRef("published-1")); !errors.Is(err, evalport.ErrCenterResourceNotFound) {
		t.Fatalf("expected tenant-safe not found, got %v", err)
	}
}

func TestKnowledgeEvaluationAdapterRejectsReassociatedCrossTenantPayload(t *testing.T) {
	snapshot := knowledgeRetrievalSnapshot{TenantID: "tenant-1", WorkspaceID: "workspace-id",
		WorkspaceName: "support", DocumentSetHash: "documents", EmbeddingIdentity: "embedding-3",
		RerankingIdentity: knowledgeRerankingIdentity, QueryMode: "vector", TopK: 5,
		Reranking: "none", QueryRewrite: "none"}
	revisions := publishedKnowledgeRevisions(t, snapshot)
	adapter := knowledgeEvaluationAdapter{revisions: revisions}

	_, err := adapter.ResolveRevision(context.Background(), "tenant-2", knowledgeRef("published-1"))
	if !errors.Is(err, evalport.ErrCenterResourceNotFound) {
		t.Fatalf("reassociated payload must be tenant-safe not found, got %v", err)
	}
	if strings.Contains(err.Error(), "tenant-1") {
		t.Fatalf("ownership rejection leaked source tenant: %v", err)
	}
}

func TestKnowledgeEvaluationAdapterCandidatePreservesTenantOwnership(t *testing.T) {
	snapshot := knowledgeRetrievalSnapshot{TenantID: "tenant-1", WorkspaceID: "workspace-id",
		WorkspaceName: "support", DocumentSetHash: "documents", EmbeddingIdentity: "embedding-3",
		RerankingIdentity: knowledgeRerankingIdentity, QueryMode: "vector", TopK: 5,
		Reranking: "none", QueryRewrite: "none"}
	revisions := publishedKnowledgeRevisions(t, snapshot)
	adapter := knowledgeEvaluationAdapter{revisions: revisions}
	_, err := adapter.CreateCandidate(context.Background(), "tenant-1", knowledgeRef("published-1"),
		evaldomain.CandidatePatch{ParameterPatch: map[string]any{"tenant_id": "tenant-2"}})
	if err == nil {
		t.Fatal("candidate changed immutable tenant ownership")
	}
}

func TestKnowledgeEvaluationAdapterSummariesPassRealRevisionValidation(t *testing.T) {
	store := &validatingRevisionStore{}
	repo := &validatingRevisionRepo{}
	revisions := evalapp.NewRevisionService(store, repo)
	source := &fakeKnowledgeSnapshotSource{workspace: &knowledgedomain.Workspace{ID: "workspace-id", Name: "support",
		Config: knowledgedomain.WorkspaceConfig{EmbeddingModel: "embedding-3", QueryMode: "hybrid", TopK: 5}}}
	adapter := knowledgeEvaluationAdapter{revisions: revisions, source: source}
	baseline, err := adapter.CreatePublishedBaseline(context.Background(), "tenant-1", "support")
	if err != nil {
		t.Fatalf("baseline rejected: %v", err)
	}
	if _, err := adapter.CreateCandidate(context.Background(), "tenant-1", baseline, evaldomain.CandidatePatch{
		ParameterPatch: map[string]any{"top_k": float64(8)},
	}); err != nil {
		t.Fatalf("candidate rejected: %v", err)
	}
}

type fakeKnowledgeRevisionService struct {
	revision     evaldomain.ResourceRevision
	payload      []byte
	found        bool
	input        evalport.CreateRevisionInput
	publishCalls int
}

func (f *fakeKnowledgeRevisionService) Get(_ context.Context, _ string, _ evaldomain.ResourceRef) (
	evaldomain.ResourceRevision, []byte, bool, error,
) {
	return f.revision, f.payload, f.found, nil
}
func (f *fakeKnowledgeRevisionService) Create(_ context.Context, _ string, in evalport.CreateRevisionInput) (
	evaldomain.ResourceRevision, bool, error,
) {
	f.input = in
	if f.revision.ID == "" {
		f.revision = evaldomain.ResourceRevision{ID: "candidate-1", ResourceKind: in.ResourceKind,
			ResourceID: in.ResourceID, Status: evaldomain.RevisionStatusDraft}
		f.found = true
		f.payload, _ = json.Marshal(in.Payload)
		return f.revision, true, nil
	}
	return f.revision, false, nil
}
func (f *fakeKnowledgeRevisionService) Publish(_ context.Context, _ string, _ evaldomain.ResourceRef) (
	evaldomain.ResourceRevision, error,
) {
	f.publishCalls++
	f.revision.Status = evaldomain.RevisionStatusPublished
	return f.revision, nil
}

func publishedKnowledgeRevisions(t *testing.T, snapshot knowledgeRetrievalSnapshot) *fakeKnowledgeRevisionService {
	t.Helper()
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeKnowledgeRevisionService{revision: evaldomain.ResourceRevision{ID: "published-1",
		ResourceKind: evaldomain.ResourceKindKnowledge, ResourceID: "support", Status: evaldomain.RevisionStatusPublished},
		payload: payload, found: true}
}

func knowledgeRef(revisionID string) evaldomain.ResourceRef {
	return evaldomain.ResourceRef{Kind: evaldomain.ResourceKindKnowledge, ResourceID: "support", RevisionID: revisionID}
}

type fakeKnowledgeSnapshotSource struct {
	workspace *knowledgedomain.Workspace
	documents []*knowledgedomain.Document
	tenantID  string
}

func (f *fakeKnowledgeSnapshotSource) GetWorkspace(_ context.Context, tenantID, _ string) (*knowledgedomain.Workspace, error) {
	f.tenantID = tenantID
	return f.workspace, nil
}
func (f *fakeKnowledgeSnapshotSource) ListSnapshotDocuments(_ context.Context, tenantID, _ string) ([]*knowledgedomain.Document, error) {
	f.tenantID = tenantID
	return f.documents, nil
}

type fakeKnowledgeRetriever struct {
	request knowledgeapp.RAGQueryRequest
	result  *knowledgeapp.RAGQueryResult
	err     error
}

func (f *fakeKnowledgeRetriever) Query(_ context.Context, request knowledgeapp.RAGQueryRequest) (*knowledgeapp.RAGQueryResult, error) {
	f.request = request
	return f.result, f.err
}
