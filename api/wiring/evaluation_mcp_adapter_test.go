package wiring

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	evalapp "github.com/byteBuilderX/stratum/internal/evaluation/application"
	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	evalport "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

func TestMCPEvaluationAdapterCreatesPublishedSafeBaseline(t *testing.T) {
	revisions := &fakeMCPRevisionService{}
	runtime := &fakeMCPRuntime{config: &mcpdomain.ServerConfig{
		ID: "server-1", Name: "orders", Transport: "streamable-http", URL: "https://secret.example/mcp",
		Auth: &mcpdomain.AuthConfig{Type: mcpdomain.AuthTypeBearer, Token: "top-secret"}, Timeout: 4 * time.Second,
		Retry: &mcpdomain.RetryConfig{Enabled: true, MaxRetries: 2},
	}, tools: []*mcpdomain.Tool{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}}}
	adapter := mcpEvaluationAdapter{runtime: runtime, revisions: revisions, actorID: "evaluation-worker"}

	ref, err := adapter.CreatePublishedBaseline(context.Background(), "tenant-1", "server-1")
	if err != nil || ref.Kind != evaldomain.ResourceKindMCP || revisions.publishCalls != 1 {
		t.Fatalf("ref=%+v publish=%d err=%v", ref, revisions.publishCalls, err)
	}
	payload, _ := json.Marshal(revisions.input.Payload)
	for _, secret := range []string{"top-secret", "secret.example", "streamable-http"} {
		if strings.Contains(string(payload), secret) {
			t.Fatalf("private snapshot leaked %q: %s", secret, payload)
		}
	}
	summary, _ := json.Marshal(revisions.input.SafeSummary)
	if strings.Contains(string(summary), "runtime_ref") || strings.Contains(string(summary), "schema") {
		t.Fatalf("unsafe summary keys: %s", summary)
	}
	if runtime.tenantID != "tenant-1" {
		t.Fatalf("tenant context not propagated: %q", runtime.tenantID)
	}
}

func TestMCPEvaluationAdapterCandidateIsExactBoundedAndIdempotent(t *testing.T) {
	snapshot := mcpRevisionSnapshot{ServerID: "server-1", Name: "orders", RuntimeRef: "server-1",
		EnabledTools: []string{"lookup", "health"}, TimeoutMS: 4000, MaxRetries: 1, ToolSchemaHash: "schema-hash"}
	revisions := publishedMCPRevisions(t, snapshot)
	adapter := mcpEvaluationAdapter{revisions: revisions, actorID: "evaluation-worker"}
	patch := evaldomain.CandidatePatch{Source: "bounded", ParameterPatch: map[string]any{
		"enabled_tools": []any{"lookup"}, "timeout_ms": float64(5000), "max_retries": float64(2),
	}}
	first, err := adapter.CreateCandidate(context.Background(), "tenant-1", mcpRef("published-1"), patch)
	if err != nil {
		t.Fatal(err)
	}
	second, err := adapter.CreateCandidate(context.Background(), "tenant-1", mcpRef("published-1"), patch)
	if err != nil || first != second || !strings.HasPrefix(revisions.input.IdempotencyKey, "mcp-candidate-") {
		t.Fatalf("first=%+v second=%+v key=%q err=%v", first, second, revisions.input.IdempotencyKey, err)
	}

	bad := []map[string]any{
		{"enabled_tools": []any{"new-tool"}}, {"timeout_ms": float64(0)}, {"max_retries": float64(100)},
		{"tool_schema_hash": "changed"}, {"credential": "changed"}, {"transport": "stdio"}, {"risk": "low"},
	}
	for _, parameterPatch := range bad {
		if _, err := adapter.CreateCandidate(context.Background(), "tenant-1", mcpRef("published-1"),
			evaldomain.CandidatePatch{Source: "bad", ParameterPatch: parameterPatch}); err == nil {
			t.Fatalf("accepted unsafe patch: %#v", parameterPatch)
		}
	}
}

func TestMCPEvaluationAdapterDetectsSchemaDriftBeforeInvocation(t *testing.T) {
	snapshot := mcpRevisionSnapshot{ServerID: "server-1", Name: "orders", RuntimeRef: "server-1",
		EnabledTools: []string{"lookup"}, TimeoutMS: 1000, ToolSchemaHash: "old"}
	revisions := publishedMCPRevisions(t, snapshot)
	runtime := &fakeMCPRuntime{tools: []*mcpdomain.Tool{{Name: "lookup", InputSchema: map[string]any{"type": "string"}}}}
	adapter := mcpEvaluationAdapter{revisions: revisions, runtime: runtime}
	_, err := adapter.ExecuteRevision(context.Background(), "tenant-1", mcpRef("published-1"), evaldomain.EvalCase{
		Input: map[string]any{"tool": "lookup", "arguments": map[string]any{"id": "1"}},
	})
	if err == nil || runtime.callCount != 0 || !strings.Contains(err.Error(), "schema_drift") {
		t.Fatalf("calls=%d err=%v", runtime.callCount, err)
	}
}

func TestMCPEvaluationAdapterInvokesExistingToolWithTenantContext(t *testing.T) {
	tools := []*mcpdomain.Tool{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}}
	hash, err := mcpToolSchemaHash(tools)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := mcpRevisionSnapshot{ServerID: "server-1", Name: "orders", RuntimeRef: "opaque",
		EnabledTools: []string{"lookup"}, TimeoutMS: 1000, ToolSchemaHash: hash}
	runtime := &fakeMCPRuntime{tools: tools, callResult: map[string]any{"private": "not persisted"}}
	adapter := mcpEvaluationAdapter{revisions: publishedMCPRevisions(t, snapshot), runtime: runtime}
	result, err := adapter.ExecuteRevision(context.Background(), "tenant-1", mcpRef("published-1"), evaldomain.EvalCase{
		Input: map[string]any{"tool": "lookup", "arguments": map[string]any{"id": "1"}},
	})
	if err != nil || runtime.callTool != "lookup" || runtime.callArguments["id"] != "1" || runtime.tenantID != "tenant-1" {
		t.Fatalf("result=%+v tool=%q arguments=%+v tenant=%q err=%v",
			result, runtime.callTool, runtime.callArguments, runtime.tenantID, err)
	}
	encoded, _ := json.Marshal(result.Output)
	if strings.Contains(string(encoded), "not persisted") {
		t.Fatalf("raw provider output escaped: %s", encoded)
	}
}

func TestMCPEvaluationAdapterSummariesPassRealRevisionValidation(t *testing.T) {
	revisions := evalapp.NewRevisionService(&validatingRevisionStore{}, &validatingRevisionRepo{})
	runtime := &fakeMCPRuntime{config: &mcpdomain.ServerConfig{
		ID: "server-1", Name: "orders", Timeout: time.Second,
	}, tools: []*mcpdomain.Tool{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}}}
	adapter := mcpEvaluationAdapter{runtime: runtime, revisions: revisions}
	baseline, err := adapter.CreatePublishedBaseline(context.Background(), "tenant-1", "server-1")
	if err != nil {
		t.Fatalf("baseline rejected by real RevisionService: %v", err)
	}
	if _, err := adapter.CreateCandidate(context.Background(), "tenant-1", baseline, evaldomain.CandidatePatch{
		ParameterPatch: map[string]any{"timeout_ms": float64(1500)},
	}); err != nil {
		t.Fatalf("candidate rejected by real RevisionService: %v", err)
	}
}

type fakeMCPRuntime struct {
	config        *mcpdomain.ServerConfig
	tools         []*mcpdomain.Tool
	callResult    any
	callErr       error
	tenantID      string
	callCount     int
	callTool      string
	callArguments map[string]any
}

func (f *fakeMCPRuntime) GetServerConfig(ctx context.Context, _ string) (*mcpdomain.ServerConfig, error) {
	if tc, ok := postgres.FromContext(ctx); ok {
		f.tenantID = tc.TenantID
	}
	if f.config == nil {
		return nil, mcpdomain.ErrServerNotFound
	}
	return f.config, nil
}
func (f *fakeMCPRuntime) ListTools(ctx context.Context, _ string) ([]*mcpdomain.Tool, error) {
	if tc, ok := postgres.FromContext(ctx); ok {
		f.tenantID = tc.TenantID
	}
	return f.tools, nil
}
func (f *fakeMCPRuntime) CallTool(ctx context.Context, _ string, tool string, input any) (any, error) {
	f.callCount++
	f.callTool = tool
	f.callArguments, _ = input.(map[string]any)
	if tc, ok := postgres.FromContext(ctx); ok {
		f.tenantID = tc.TenantID
	}
	return f.callResult, f.callErr
}

type fakeMCPRevisionService struct {
	revision     evaldomain.ResourceRevision
	payload      []byte
	found        bool
	input        evalport.CreateRevisionInput
	createErr    error
	publishCalls int
}

func (f *fakeMCPRevisionService) Get(context.Context, string, evaldomain.ResourceRef) (evaldomain.ResourceRevision, []byte, bool, error) {
	return f.revision, f.payload, f.found, nil
}
func (f *fakeMCPRevisionService) Create(_ context.Context, _ string, input evalport.CreateRevisionInput) (evaldomain.ResourceRevision, bool, error) {
	f.input = input
	if f.createErr != nil {
		return evaldomain.ResourceRevision{}, false, f.createErr
	}
	if f.revision.ID == "" {
		f.revision = evaldomain.ResourceRevision{ID: "created-1", ResourceKind: input.ResourceKind, ResourceID: input.ResourceID, Status: evaldomain.RevisionStatusDraft}
	}
	return f.revision, true, nil
}
func (f *fakeMCPRevisionService) Publish(_ context.Context, _ string, ref evaldomain.ResourceRef) (evaldomain.ResourceRevision, error) {
	f.publishCalls++
	f.revision.ID, f.revision.Status = ref.RevisionID, evaldomain.RevisionStatusPublished
	return f.revision, nil
}

func publishedMCPRevisions(t *testing.T, snapshot mcpRevisionSnapshot) *fakeMCPRevisionService {
	t.Helper()
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeMCPRevisionService{revision: evaldomain.ResourceRevision{ID: "published-1", ResourceKind: evaldomain.ResourceKindMCP,
		ResourceID: "server-1", Status: evaldomain.RevisionStatusPublished}, payload: payload, found: true}
}
func mcpRef(revisionID string) evaldomain.ResourceRef {
	return evaldomain.ResourceRef{Kind: evaldomain.ResourceKindMCP, ResourceID: "server-1", RevisionID: revisionID}
}
