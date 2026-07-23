package wiring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	evalport "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/google/uuid"
)

type agentRevisionService interface {
	Get(context.Context, string, evaldomain.ResourceRef) (evaldomain.ResourceRevision, []byte, bool, error)
	Create(context.Context, string, evalport.CreateRevisionInput) (evaldomain.ResourceRevision, bool, error)
	Publish(context.Context, string, evaldomain.ResourceRef) (evaldomain.ResourceRevision, error)
}

type agentRevisionExecutor interface {
	SnapshotRevision(context.Context, string, string) (agentdomain.AgentRevision, error)
	ExecuteRevision(context.Context, agentdomain.AgentRevision, agentapp.ExecRequest, agentapp.ExecMeta) (*agentapp.AgentResult, int, error)
}

type agentEvaluationAdapter struct {
	revisions agentRevisionService
	agents    agentRevisionExecutor
	actorID   string
}

func (a agentEvaluationAdapter) CreatePublishedBaseline(
	ctx context.Context, tenantID, agentID string,
) (evaldomain.ResourceRef, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(agentID) == "" {
		return evaldomain.ResourceRef{}, errors.New("evaluation Agent adapter: tenant and agent IDs required")
	}
	if a.agents == nil {
		return evaldomain.ResourceRef{}, errors.New("evaluation Agent adapter: Agent service unavailable")
	}
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{
		TenantID: tenantID, UserID: "evaluation-worker", Role: postgres.RoleTenantAdmin,
	})
	snapshot, err := a.agents.SnapshotRevision(ctx, tenantID, agentID)
	if err != nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation Agent adapter: snapshot baseline: %w", err)
	}
	contentHash, err := snapshot.ContentHash()
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	actorID := strings.TrimSpace(a.actorID)
	if actorID == "" {
		actorID = "evaluation-worker"
	}
	created, _, err := a.revisions.Create(ctx, tenantID, evalport.CreateRevisionInput{
		ResourceKind: evaldomain.ResourceKindAgent, ResourceID: agentID,
		CreatedBy: actorID, IdempotencyKey: "agent-baseline-" + contentHash,
		Source: evaldomain.RevisionSourceManual, Payload: snapshot, SafeSummary: snapshot.SafeSummary(),
	})
	if err != nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation Agent adapter: create baseline: %w", err)
	}
	ref := evaldomain.ResourceRef{Kind: evaldomain.ResourceKindAgent, ResourceID: agentID, RevisionID: created.ID}
	if _, err := a.revisions.Publish(ctx, tenantID, ref); err != nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation Agent adapter: publish baseline: %w", err)
	}
	return ref, nil
}

func (a agentEvaluationAdapter) LoadOptimizableSnapshot(
	ctx context.Context, tenantID string, baseline evaldomain.ResourceRef,
) (map[string]any, error) {
	_, snapshot, err := a.loadPublished(ctx, tenantID, baseline)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("evaluation Agent adapter: encode snapshot: %w", err)
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, fmt.Errorf("evaluation Agent adapter: map snapshot: %w", err)
	}
	return result, nil
}

func (a agentEvaluationAdapter) CreateCandidate(
	ctx context.Context, tenantID string, baseline evaldomain.ResourceRef, patch evaldomain.CandidatePatch,
) (evaldomain.ResourceRef, error) {
	parent, snapshot, err := a.loadPublished(ctx, tenantID, baseline)
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	candidatePatch, err := parseAgentCandidatePatch(snapshot, patch)
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	candidate, err := snapshot.ApplyCandidate(candidatePatch)
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	contentHash, err := candidate.ContentHash()
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	actorID := strings.TrimSpace(a.actorID)
	if actorID == "" {
		actorID = "evaluation-worker"
	}
	stored, _, err := a.revisions.Create(ctx, tenantID, evalport.CreateRevisionInput{
		ResourceKind: evaldomain.ResourceKindAgent, ResourceID: baseline.ResourceID,
		ParentRevisionID: parent.ID, CreatedBy: actorID,
		IdempotencyKey: agentCandidateIdempotencyKey(tenantID, baseline, contentHash),
		Source:         evaldomain.RevisionSourceOptimization, Payload: candidate,
		SafeSummary: agentCandidateSafeSummary(snapshot, candidate),
	})
	if err != nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation Agent adapter: create candidate: %w", err)
	}
	return evaldomain.ResourceRef{Kind: evaldomain.ResourceKindAgent, ResourceID: baseline.ResourceID, RevisionID: stored.ID}, nil
}

func (a agentEvaluationAdapter) ResolveRevision(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef,
) (evaldomain.ResourceRevision, error) {
	revision, _, found, err := a.get(ctx, tenantID, ref)
	if err != nil {
		return evaldomain.ResourceRevision{}, err
	}
	if !found {
		return evaldomain.ResourceRevision{}, evalport.ErrCenterResourceNotFound
	}
	if revision.Status != evaldomain.RevisionStatusPublished {
		return evaldomain.ResourceRevision{}, evaldomain.ErrRevisionNotPublished
	}
	return revision, nil
}

func (a agentEvaluationAdapter) SafeSummary(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef,
) (map[string]any, error) {
	revision, err := a.ResolveRevision(ctx, tenantID, ref)
	if err != nil {
		return nil, err
	}
	return revision.SafeSummary, nil
}

func (a agentEvaluationAdapter) ExecuteRevision(
	ctx context.Context, tenantID, requestedBy string, ref evaldomain.ResourceRef, testCase evaldomain.EvalCase,
) (evalport.ExecutionResult, error) {
	if a.agents == nil {
		return evalport.ExecutionResult{}, errors.New("evaluation Agent adapter: executor unavailable")
	}
	ctx, err := evaluationAgentContext(ctx, tenantID, requestedBy)
	if err != nil {
		return evalport.ExecutionResult{}, err
	}
	revision, snapshot, found, err := a.get(ctx, tenantID, ref)
	if err != nil {
		return evalport.ExecutionResult{}, err
	}
	if !found {
		return evalport.ExecutionResult{}, evalport.ErrCenterResourceNotFound
	}
	if revision.Status != evaldomain.RevisionStatusPublished {
		return evalport.ExecutionResult{}, evaldomain.ErrRevisionNotPublished
	}
	query, err := evaluationCaseQuery(testCase.Input)
	if err != nil {
		return evalport.ExecutionResult{}, err
	}
	traceID := uuid.Must(uuid.NewV7()).String()
	result, duration, err := a.agents.ExecuteRevision(ctx, snapshot,
		agentapp.ExecRequest{Query: query, UserID: requestedBy}, agentapp.ExecMeta{
			TenantID: tenantID, TraceID: traceID,
			EvolutionTrace: agentapp.EvolutionTraceMetadata{Evaluation: true,
				ResourceManifest: map[string]string{"agent:" + ref.ResourceID: ref.RevisionID}},
		})
	if err != nil {
		return evalport.ExecutionResult{}, fmt.Errorf("evaluation Agent adapter: execute revision: %w", err)
	}
	if result == nil {
		return evalport.ExecutionResult{}, errors.New("evaluation Agent adapter: provider returned no result")
	}
	return evalport.ExecutionResult{Output: result.Output, TraceID: traceID, Tokens: result.TokensUsed,
		CostUSD: result.CostUSD, DurationMs: duration}, nil
}

func (a agentEvaluationAdapter) loadPublished(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef,
) (evaldomain.ResourceRevision, agentdomain.AgentRevision, error) {
	revision, snapshot, found, err := a.get(ctx, tenantID, ref)
	if err != nil {
		return evaldomain.ResourceRevision{}, agentdomain.AgentRevision{}, err
	}
	if !found {
		return evaldomain.ResourceRevision{}, agentdomain.AgentRevision{}, evalport.ErrCenterResourceNotFound
	}
	if revision.Status != evaldomain.RevisionStatusPublished {
		return evaldomain.ResourceRevision{}, agentdomain.AgentRevision{}, evaldomain.ErrRevisionNotPublished
	}
	return revision, snapshot, nil
}

func (a agentEvaluationAdapter) get(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef,
) (evaldomain.ResourceRevision, agentdomain.AgentRevision, bool, error) {
	if strings.TrimSpace(tenantID) == "" {
		return evaldomain.ResourceRevision{}, agentdomain.AgentRevision{}, false,
			errors.New("evaluation Agent adapter: tenant ID required")
	}
	if ref.Kind != evaldomain.ResourceKindAgent {
		return evaldomain.ResourceRevision{}, agentdomain.AgentRevision{}, false,
			fmt.Errorf("evaluation Agent adapter: unsupported resource kind %q", ref.Kind)
	}
	if err := ref.Validate(); err != nil {
		return evaldomain.ResourceRevision{}, agentdomain.AgentRevision{}, false, err
	}
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{
		TenantID: tenantID, UserID: "evaluation-worker", Role: postgres.RoleTenantAdmin,
	})
	revision, payload, found, err := a.revisions.Get(ctx, tenantID, ref)
	if err != nil || !found {
		return revision, agentdomain.AgentRevision{}, found, err
	}
	var snapshot agentdomain.AgentRevision
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return evaldomain.ResourceRevision{}, agentdomain.AgentRevision{}, false,
			fmt.Errorf("evaluation Agent adapter: decode revision: %w", err)
	}
	if snapshot.AgentID != ref.ResourceID {
		return evaldomain.ResourceRevision{}, agentdomain.AgentRevision{}, false,
			evalport.ErrCenterResourceNotFound
	}
	if err := snapshot.Validate(); err != nil {
		return evaldomain.ResourceRevision{}, agentdomain.AgentRevision{}, false, err
	}
	return revision, snapshot, true, nil
}

func parseAgentCandidatePatch(
	baseline agentdomain.AgentRevision, patch evaldomain.CandidatePatch,
) (agentdomain.AgentCandidatePatch, error) {
	result := agentdomain.AgentCandidatePatch{}
	for key, value := range patch.PromptPatch {
		if key != "instructions" {
			return result, fmt.Errorf("evaluation Agent adapter: prompt field is not optimizable: %s", key)
		}
		prompt, ok := value.(string)
		if !ok || strings.TrimSpace(prompt) == "" {
			return result, errors.New("evaluation Agent adapter: instructions must be non-empty")
		}
		result.SystemPrompt = prompt
	}
	params := baseline.ModelParameters
	parametersChanged := false
	for key, value := range patch.ParameterPatch {
		switch key {
		case "model":
			result.Model, _ = value.(string)
			if strings.TrimSpace(result.Model) == "" {
				return result, errors.New("evaluation Agent adapter: model must be non-empty")
			}
		case "max_context_tokens":
			parsed, ok := integer(value)
			if !ok {
				return result, errors.New("evaluation Agent adapter: max_context_tokens must be an integer")
			}
			params.MaxContextTokens, parametersChanged = parsed, true
		case "max_iterations":
			parsed, ok := integer(value)
			if !ok {
				return result, errors.New("evaluation Agent adapter: max_iterations must be an integer")
			}
			result.MaxIterations = parsed
		case "bindings":
			bindings, err := bindingPatch(value)
			if err != nil {
				return result, err
			}
			result.Bindings = bindings
		default:
			return result, fmt.Errorf("evaluation Agent adapter: parameter field is not optimizable: %s", key)
		}
	}
	if parametersChanged {
		result.ModelParameters = &params
	}
	return result, nil
}

func bindingPatch(value any) ([]agentdomain.AgentBinding, error) {
	states, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("evaluation Agent adapter: bindings must be an object")
	}
	bindings := make([]agentdomain.AgentBinding, 0, len(states))
	for key, rawEnabled := range states {
		kindText, id, ok := strings.Cut(key, ":")
		enabled, enabledOK := rawEnabled.(bool)
		if !ok || strings.TrimSpace(id) == "" || !enabledOK {
			return nil, fmt.Errorf("evaluation Agent adapter: invalid binding patch %q", key)
		}
		bindings = append(bindings, agentdomain.AgentBinding{Kind: agentdomain.AgentBindingKind(kindText), ID: id, Enabled: enabled})
	}
	return bindings, nil
}

func integer(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case float64:
		converted := int(typed)
		return converted, float64(converted) == typed
	default:
		return 0, false
	}
}

func agentCandidateIdempotencyKey(tenantID string, baseline evaldomain.ResourceRef, contentHash string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{tenantID, string(baseline.Kind), baseline.ResourceID,
		baseline.RevisionID, contentHash}, "\x00")))
	return "agent-candidate-" + hex.EncodeToString(sum[:])
}

func agentCandidateSafeSummary(baseline, candidate agentdomain.AgentRevision) map[string]any {
	changed := make([]string, 0, 4)
	types := make([]string, 0, 4)
	appendChange := func(field, changeType string, condition bool) {
		if condition {
			changed = append(changed, field)
			types = append(types, changeType)
		}
	}
	appendChange("system_prompt", "modified", baseline.SystemPrompt != candidate.SystemPrompt)
	appendChange("model", "modified", baseline.Model != candidate.Model)
	appendChange("model_parameters", "modified", baseline.ModelParameters != candidate.ModelParameters)
	appendChange("max_iterations", "modified", baseline.MaxIterations != candidate.MaxIterations)
	appendChange("bindings", "modified", !bindingsEqual(baseline.Bindings, candidate.Bindings))
	return map[string]any{
		"resource_name":  candidate.AgentID,
		"version_label":  "candidate",
		"changed_fields": changed,
		"change_types":   types,
	}
}

func evaluationAgentContext(ctx context.Context, tenantID, requestedBy string) (context.Context, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, errors.New("evaluation Agent adapter: tenant ID required")
	}
	if strings.TrimSpace(requestedBy) == "" {
		return nil, errors.New("evaluation Agent adapter: requesting user ID required")
	}
	return postgres.WithTenant(ctx, &postgres.TenantContext{
		TenantID: tenantID, UserID: requestedBy, Role: postgres.RoleTenantAdmin,
	}), nil
}

func bindingsEqual(left, right []agentdomain.AgentBinding) bool {
	leftRevision := agentdomain.AgentRevision{AgentID: "compare", Type: agentdomain.ReActAgent,
		SystemPrompt: "compare", Model: "compare", MaxIterations: 1, Bindings: left}
	rightRevision := leftRevision
	rightRevision.Bindings = right
	leftHash, leftErr := leftRevision.ContentHash()
	rightHash, rightErr := rightRevision.ContentHash()
	return leftErr == nil && rightErr == nil && leftHash == rightHash
}

func evaluationCaseQuery(input any) (string, error) {
	if text, ok := input.(string); ok {
		return text, nil
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("evaluation Agent adapter: encode input: %w", err)
	}
	return string(payload), nil
}

var _ evalport.ResourceAdapter = agentEvaluationAdapter{}
var _ evalport.CandidateCreator = agentEvaluationAdapter{}
var _ evalport.AgentRevisionProvider = agentEvaluationAdapter{}
