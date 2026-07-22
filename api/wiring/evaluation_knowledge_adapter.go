package wiring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	evalport "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	knowledgeapp "github.com/byteBuilderX/stratum/internal/knowledge/application"
	knowledgedomain "github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

const (
	knowledgeDefaultScoreThreshold = 0.0
	knowledgeDefaultReranking      = knowledgeapp.RerankingNone
	knowledgeDefaultQueryRewrite   = knowledgeapp.QueryRewriteNone
	knowledgeRerankingIdentity     = "builtin-score-v1"
)

type knowledgeRevisionService interface {
	Get(context.Context, string, evaldomain.ResourceRef) (evaldomain.ResourceRevision, []byte, bool, error)
	Create(context.Context, string, evalport.CreateRevisionInput) (evaldomain.ResourceRevision, bool, error)
	Publish(context.Context, string, evaldomain.ResourceRef) (evaldomain.ResourceRevision, error)
}

type knowledgeSnapshotSource interface {
	GetWorkspace(context.Context, string, string) (*knowledgedomain.Workspace, error)
	ListSnapshotDocuments(context.Context, string, string) ([]*knowledgedomain.Document, error)
}

type knowledgeRetrievalSnapshot struct {
	TenantID          string  `json:"tenant_id"`
	WorkspaceID       string  `json:"workspace_id"`
	WorkspaceName     string  `json:"workspace_name"`
	DocumentSetHash   string  `json:"document_set_hash"`
	EmbeddingIdentity string  `json:"embedding_identity"`
	RerankingIdentity string  `json:"reranking_identity"`
	QueryMode         string  `json:"query_mode"`
	TopK              int     `json:"top_k"`
	ScoreThreshold    float64 `json:"score_threshold"`
	Reranking         string  `json:"reranking"`
	QueryRewrite      string  `json:"query_rewrite"`
}

func (s knowledgeRetrievalSnapshot) evaluationSnapshot() knowledgeapp.RetrievalSnapshot {
	return knowledgeapp.RetrievalSnapshot{WorkspaceID: s.WorkspaceID, WorkspaceName: s.WorkspaceName,
		EmbeddingModel: s.EmbeddingIdentity, QueryMode: s.QueryMode, TopK: s.TopK,
		ScoreThreshold: s.ScoreThreshold, Reranking: s.Reranking, QueryRewrite: s.QueryRewrite}
}

func (s knowledgeRetrievalSnapshot) validate() error {
	if strings.TrimSpace(s.TenantID) == "" || strings.TrimSpace(s.DocumentSetHash) == "" ||
		strings.TrimSpace(s.RerankingIdentity) == "" {
		return errors.New("evaluation Knowledge adapter: tenant, document, and reranking identities required")
	}
	return s.evaluationSnapshot().Validate()
}

type knowledgeEvaluationAdapter struct {
	revisions knowledgeRevisionService
	source    knowledgeSnapshotSource
	evaluator *knowledgeapp.RetrievalEvaluator
	actorID   string
}

func (a knowledgeEvaluationAdapter) CreatePublishedBaseline(
	ctx context.Context, tenantID, workspaceName string,
) (evaldomain.ResourceRef, error) {
	ctx, err := evaluationKnowledgeContext(ctx, tenantID)
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	if strings.TrimSpace(workspaceName) == "" || a.revisions == nil || a.source == nil {
		return evaldomain.ResourceRef{}, errors.New("evaluation Knowledge adapter: baseline dependencies required")
	}
	workspace, err := a.source.GetWorkspace(ctx, tenantID, workspaceName)
	if err != nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation Knowledge adapter: load workspace: %w", err)
	}
	if workspace == nil || strings.TrimSpace(workspace.ID) == "" || workspace.Name != workspaceName {
		return evaldomain.ResourceRef{}, evalport.ErrCenterResourceNotFound
	}
	documents, err := a.source.ListSnapshotDocuments(ctx, tenantID, workspace.ID)
	if err != nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation Knowledge adapter: list documents: %w", err)
	}
	documentSetHash, err := knowledgeDocumentSetHash(documents)
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	snapshot := knowledgeRetrievalSnapshot{TenantID: tenantID, WorkspaceID: workspace.ID, WorkspaceName: workspace.Name,
		DocumentSetHash: documentSetHash, EmbeddingIdentity: workspace.Config.EmbeddingModel,
		RerankingIdentity: knowledgeRerankingIdentity, QueryMode: workspace.Config.QueryMode,
		TopK: workspace.Config.TopK, ScoreThreshold: knowledgeDefaultScoreThreshold,
		Reranking: knowledgeDefaultReranking, QueryRewrite: knowledgeDefaultQueryRewrite}
	if err := snapshot.validate(); err != nil {
		return evaldomain.ResourceRef{}, err
	}
	contentHash, err := canonicalKnowledgeHash(snapshot)
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	actorID := strings.TrimSpace(a.actorID)
	if actorID == "" {
		actorID = "evaluation-worker"
	}
	created, _, err := a.revisions.Create(ctx, tenantID, evalport.CreateRevisionInput{
		ResourceKind: evaldomain.ResourceKindKnowledge, ResourceID: workspaceName, CreatedBy: actorID,
		IdempotencyKey:     "knowledge-baseline-" + stableTenantFingerprint(tenantID, contentHash),
		FingerprintPayload: snapshot, Source: evaldomain.RevisionSourceManual, Payload: snapshot,
		SafeSummary: map[string]any{"resource_name": workspace.Name},
	})
	if err != nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation Knowledge adapter: create baseline: %w", err)
	}
	ref := evaldomain.ResourceRef{Kind: evaldomain.ResourceKindKnowledge, ResourceID: workspaceName,
		RevisionID: created.ID}
	if _, err := a.revisions.Publish(ctx, tenantID, ref); err != nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation Knowledge adapter: publish baseline: %w", err)
	}
	return ref, nil
}

func (a knowledgeEvaluationAdapter) LoadOptimizableSnapshot(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef,
) (map[string]any, error) {
	_, snapshot, err := a.loadPublished(ctx, tenantID, ref)
	if err != nil {
		return nil, err
	}
	return map[string]any{"top_k": snapshot.TopK, "score_threshold": snapshot.ScoreThreshold,
		"reranking": snapshot.Reranking, "query_rewrite": snapshot.QueryRewrite}, nil
}

func (a knowledgeEvaluationAdapter) CreateCandidate(
	ctx context.Context, tenantID string, baseline evaldomain.ResourceRef, patch evaldomain.CandidatePatch,
) (evaldomain.ResourceRef, error) {
	parent, snapshot, err := a.loadPublished(ctx, tenantID, baseline)
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	changedFields, err := applyKnowledgeCandidatePatch(&snapshot, patch)
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	contentHash, err := canonicalKnowledgeHash(snapshot)
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	actorID := strings.TrimSpace(a.actorID)
	if actorID == "" {
		actorID = "evaluation-worker"
	}
	changeTypes := make([]string, len(changedFields))
	for i := range changeTypes {
		changeTypes[i] = "modified"
	}
	created, _, err := a.revisions.Create(ctx, tenantID, evalport.CreateRevisionInput{
		ResourceKind: evaldomain.ResourceKindKnowledge, ResourceID: baseline.ResourceID,
		ParentRevisionID: parent.ID, CreatedBy: actorID,
		IdempotencyKey: "knowledge-candidate-" + stableTenantFingerprint(tenantID,
			baseline.RevisionID+":"+contentHash),
		Source: evaldomain.RevisionSourceOptimization, Payload: snapshot,
		SafeSummary: map[string]any{"resource_name": snapshot.WorkspaceName,
			"changed_fields": changedFields, "change_types": changeTypes},
	})
	if err != nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation Knowledge adapter: create candidate: %w", err)
	}
	return evaldomain.ResourceRef{Kind: evaldomain.ResourceKindKnowledge, ResourceID: baseline.ResourceID,
		RevisionID: created.ID}, nil
}

func (a knowledgeEvaluationAdapter) ResolveRevision(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef,
) (evaldomain.ResourceRevision, error) {
	revision, _, err := a.loadPublished(ctx, tenantID, ref)
	return revision, err
}

func (a knowledgeEvaluationAdapter) SafeSummary(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef,
) (map[string]any, error) {
	revision, err := a.ResolveRevision(ctx, tenantID, ref)
	if err != nil {
		return nil, err
	}
	return revision.SafeSummary, nil
}

func (a knowledgeEvaluationAdapter) ExecuteRevision(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef, testCase evaldomain.EvalCase,
) (evalport.ExecutionResult, error) {
	if a.evaluator == nil || a.source == nil {
		return evalport.ExecutionResult{}, errors.New("evaluation Knowledge adapter: evaluator unavailable")
	}
	ctx, err := evaluationKnowledgeContext(ctx, tenantID)
	if err != nil {
		return evalport.ExecutionResult{}, err
	}
	_, snapshot, err := a.loadPublished(ctx, tenantID, ref)
	if err != nil {
		return evalport.ExecutionResult{}, err
	}
	documents, err := a.source.ListSnapshotDocuments(ctx, tenantID, snapshot.WorkspaceID)
	if err != nil {
		return evalport.ExecutionResult{}, fmt.Errorf("evaluation Knowledge adapter: verify documents: %w", err)
	}
	currentHash, err := knowledgeDocumentSetHash(documents)
	if err != nil {
		return evalport.ExecutionResult{}, err
	}
	if currentHash != snapshot.DocumentSetHash {
		return evalport.ExecutionResult{}, errors.New("evaluation Knowledge adapter: document set changed")
	}
	retrievalCase, err := parseKnowledgeRetrievalCase(testCase)
	if err != nil {
		return evalport.ExecutionResult{}, err
	}
	result, err := a.evaluator.EvaluateRetrieval(ctx, snapshot.evaluationSnapshot(), retrievalCase)
	if err != nil {
		return evalport.ExecutionResult{}, err
	}
	return evalport.ExecutionResult{Output: result}, nil
}

func (a knowledgeEvaluationAdapter) loadPublished(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef,
) (evaldomain.ResourceRevision, knowledgeRetrievalSnapshot, error) {
	ctx, err := evaluationKnowledgeContext(ctx, tenantID)
	if err != nil {
		return evaldomain.ResourceRevision{}, knowledgeRetrievalSnapshot{}, err
	}
	if a.revisions == nil {
		return evaldomain.ResourceRevision{}, knowledgeRetrievalSnapshot{},
			errors.New("evaluation Knowledge adapter: revisions unavailable")
	}
	if ref.Kind != evaldomain.ResourceKindKnowledge {
		return evaldomain.ResourceRevision{}, knowledgeRetrievalSnapshot{},
			fmt.Errorf("evaluation Knowledge adapter: unsupported resource kind %q", ref.Kind)
	}
	if err := ref.Validate(); err != nil {
		return evaldomain.ResourceRevision{}, knowledgeRetrievalSnapshot{}, err
	}
	revision, payload, found, err := a.revisions.Get(ctx, tenantID, ref)
	if err != nil {
		return evaldomain.ResourceRevision{}, knowledgeRetrievalSnapshot{}, err
	}
	if !found || revision.ResourceKind != ref.Kind || revision.ResourceID != ref.ResourceID {
		return evaldomain.ResourceRevision{}, knowledgeRetrievalSnapshot{}, evalport.ErrCenterResourceNotFound
	}
	if revision.Status != evaldomain.RevisionStatusPublished {
		return evaldomain.ResourceRevision{}, knowledgeRetrievalSnapshot{}, evaldomain.ErrRevisionNotPublished
	}
	var snapshot knowledgeRetrievalSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return evaldomain.ResourceRevision{}, knowledgeRetrievalSnapshot{},
			fmt.Errorf("evaluation Knowledge adapter: decode revision: %w", err)
	}
	if snapshot.WorkspaceName != ref.ResourceID {
		return evaldomain.ResourceRevision{}, knowledgeRetrievalSnapshot{}, evalport.ErrCenterResourceNotFound
	}
	if snapshot.TenantID != tenantID {
		return evaldomain.ResourceRevision{}, knowledgeRetrievalSnapshot{}, evalport.ErrCenterResourceNotFound
	}
	if err := snapshot.validate(); err != nil {
		return evaldomain.ResourceRevision{}, knowledgeRetrievalSnapshot{}, err
	}
	return revision, snapshot, nil
}

func evaluationKnowledgeContext(ctx context.Context, tenantID string) (context.Context, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, errors.New("evaluation Knowledge adapter: tenant ID required")
	}
	ctx = reqctx.WithTenantID(ctx, tenantID)
	return postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID,
		UserID: "evaluation-worker", Role: postgres.RoleTenantAdmin}), nil
}

func knowledgeDocumentSetHash(documents []*knowledgedomain.Document) (string, error) {
	type documentFingerprint struct {
		ID, ContentHash, Status string
	}
	items := make([]documentFingerprint, 0, len(documents))
	for _, document := range documents {
		if document == nil || strings.TrimSpace(document.ID) == "" || strings.TrimSpace(document.ContentHash) == "" {
			return "", errors.New("evaluation Knowledge adapter: incomplete document metadata")
		}
		items = append(items, documentFingerprint{ID: document.ID, ContentHash: document.ContentHash,
			Status: document.IngestStatus})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return canonicalKnowledgeHash(items)
}

func canonicalKnowledgeHash(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("evaluation Knowledge adapter: encode fingerprint: %w", err)
	}
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:]), nil
}

func applyKnowledgeCandidatePatch(snapshot *knowledgeRetrievalSnapshot, patch evaldomain.CandidatePatch) ([]string, error) {
	if snapshot == nil {
		return nil, errors.New("evaluation Knowledge adapter: snapshot required")
	}
	if len(patch.PromptPatch) != 0 {
		return nil, errors.New("evaluation Knowledge adapter: prompt patch is not supported")
	}
	allowed := map[string]bool{"top_k": true, "score_threshold": true, "reranking": true, "query_rewrite": true}
	changed := make([]string, 0, len(patch.ParameterPatch))
	for key, value := range patch.ParameterPatch {
		if !allowed[key] {
			return nil, fmt.Errorf("evaluation Knowledge adapter: parameter %q is not optimizable", key)
		}
		switch key {
		case "top_k":
			parsed, err := knowledgeInteger(value)
			if err != nil {
				return nil, fmt.Errorf("evaluation Knowledge adapter: top_k: %w", err)
			}
			snapshot.TopK = parsed
		case "score_threshold":
			parsed, ok := value.(float64)
			if !ok {
				return nil, errors.New("evaluation Knowledge adapter: score_threshold must be numeric")
			}
			snapshot.ScoreThreshold = parsed
		case "reranking":
			parsed, ok := value.(string)
			if !ok {
				return nil, errors.New("evaluation Knowledge adapter: reranking must be string")
			}
			snapshot.Reranking = parsed
		case "query_rewrite":
			parsed, ok := value.(string)
			if !ok {
				return nil, errors.New("evaluation Knowledge adapter: query_rewrite must be string")
			}
			snapshot.QueryRewrite = parsed
		}
		changed = append(changed, key)
	}
	if len(changed) == 0 {
		return nil, errors.New("evaluation Knowledge adapter: candidate patch required")
	}
	if err := snapshot.validate(); err != nil {
		return nil, err
	}
	sort.Strings(changed)
	return changed, nil
}

func knowledgeInteger(value any) (int, error) {
	number, ok := value.(float64)
	if !ok || number != float64(int(number)) {
		return 0, errors.New("must be an integer")
	}
	return int(number), nil
}

func parseKnowledgeRetrievalCase(testCase evaldomain.EvalCase) (knowledgeapp.RetrievalCase, error) {
	input, ok := testCase.Input.(map[string]any)
	if !ok {
		query, ok := testCase.Input.(string)
		if !ok || strings.TrimSpace(query) == "" {
			return knowledgeapp.RetrievalCase{}, errors.New("evaluation Knowledge adapter: case query required")
		}
		return knowledgeapp.RetrievalCase{Query: query}, nil
	}
	query, _ := input["query"].(string)
	if strings.TrimSpace(query) == "" {
		return knowledgeapp.RetrievalCase{}, errors.New("evaluation Knowledge adapter: case query required")
	}
	relevant, err := knowledgeStringSlice(input["relevant_document_ids"])
	if err != nil {
		return knowledgeapp.RetrievalCase{}, err
	}
	citations, err := knowledgeStringSlice(input["citation_document_ids"])
	if err != nil {
		return knowledgeapp.RetrievalCase{}, err
	}
	expectNoAnswer, _ := input["expect_no_answer"].(bool)
	return knowledgeapp.RetrievalCase{Query: query, RelevantDocumentIDs: relevant,
		CitationDocumentIDs: citations, ExpectNoAnswer: expectNoAnswer}, nil
}

func knowledgeStringSlice(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	values, ok := value.([]any)
	if !ok {
		return nil, errors.New("evaluation Knowledge adapter: document IDs must be an array")
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, errors.New("evaluation Knowledge adapter: document ID must be a string")
		}
		result = append(result, text)
	}
	return result, nil
}
