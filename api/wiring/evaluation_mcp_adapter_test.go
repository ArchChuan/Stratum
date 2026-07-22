package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	evalapp "github.com/byteBuilderX/stratum/internal/evaluation/application"
	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	evalport "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	pkgobjectstore "github.com/byteBuilderX/stratum/pkg/storage/objectstore"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

func TestMCPEvaluationAdapterCreatesPublishedSafeBaseline(t *testing.T) {
	revisions := &fakeMCPRevisionService{}
	runtime := &fakeMCPRuntime{config: &mcpdomain.ServerConfig{
		ID: "server-1", Name: "orders", Transport: "streamable-http", URL: "https://secret.example/mcp",
		Auth: &mcpdomain.AuthConfig{Type: mcpdomain.AuthTypeBearer, Token: "top-secret"}, Timeout: 4 * time.Second,
		Retry: &mcpdomain.RetryConfig{Enabled: true, MaxRetries: 2},
	}, tools: []*mcpdomain.Tool{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}}}
	runtimeStore := &fakeMCPRuntimeStore{}
	adapter := mcpEvaluationAdapter{runtime: runtime, revisions: revisions, runtimeStore: runtimeStore,
		actorID: "evaluation-worker"}

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
	storedConfig := runtimeStore.lastValue.(mcpRuntimeConfigEnvelope).Config
	if storedConfig.URL != "https://secret.example/mcp" || storedConfig.Auth.Token != "top-secret" {
		t.Fatal("runtime config was not delegated to encrypted object storage")
	}
	summary, _ := json.Marshal(revisions.input.SafeSummary)
	if strings.Contains(string(summary), "runtime_ref") || strings.Contains(string(summary), "schema") {
		t.Fatalf("unsafe summary keys: %s", summary)
	}
	if runtime.tenantID != "tenant-1" {
		t.Fatalf("tenant context not propagated: %q", runtime.tenantID)
	}
}

func TestMCPEvaluationAdapterBaselineReplayIsStableAndCleansDuplicateRuntime(t *testing.T) {
	revisions := &fakeMCPRevisionService{}
	store := &fakeMCPRuntimeStore{}
	runtime := &fakeMCPRuntime{config: &mcpdomain.ServerConfig{
		ID: "server-1", Name: "orders", Transport: "http", URL: "https://one.example", Timeout: time.Second,
	}, tools: []*mcpdomain.Tool{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}}}
	adapter := mcpEvaluationAdapter{runtime: runtime, revisions: revisions, runtimeStore: store}
	first, err := adapter.CreatePublishedBaseline(context.Background(), "tenant-1", "server-1")
	if err != nil {
		t.Fatal(err)
	}
	firstKey := revisions.input.IdempotencyKey
	second, err := adapter.CreatePublishedBaseline(context.Background(), "tenant-1", "server-1")
	if err != nil || first != second || revisions.input.IdempotencyKey != firstKey || store.putCalls != 2 ||
		len(store.deleted) != 1 || store.deleted[0] != store.putRefs[1].URI ||
		store.deleted[0] == store.putRefs[0].URI {
		t.Fatalf("first=%+v second=%+v key=%q puts=%d deleted=%v err=%v",
			first, second, revisions.input.IdempotencyKey, store.putCalls, store.deleted, err)
	}
}

func TestMCPEvaluationAdapterBaselineFailureCleansRuntimeObject(t *testing.T) {
	runtime := &fakeMCPRuntime{config: &mcpdomain.ServerConfig{
		ID: "server-1", Name: "orders", Timeout: time.Second,
	}, tools: []*mcpdomain.Tool{{Name: "lookup"}}}
	for _, tc := range []struct {
		name        string
		revisions   *fakeMCPRevisionService
		storePutErr error
		wantDeletes int
	}{
		{name: "upload", revisions: &fakeMCPRevisionService{}, storePutErr: errors.New("upload failed")},
		{name: "create", revisions: &fakeMCPRevisionService{createErr: errors.New("create failed")}, wantDeletes: 1},
		{name: "publish", revisions: &fakeMCPRevisionService{publishErr: errors.New("publish failed")}, wantDeletes: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeMCPRuntimeStore{putErr: tc.storePutErr}
			adapter := mcpEvaluationAdapter{runtime: runtime, revisions: tc.revisions, runtimeStore: store}
			if _, err := adapter.CreatePublishedBaseline(context.Background(), "tenant-1", "server-1"); err == nil {
				t.Fatal("expected baseline failure")
			}
			if len(store.deleted) != tc.wantDeletes {
				t.Fatalf("deleted=%v want=%d", store.deleted, tc.wantDeletes)
			}
		})
	}
}

func TestMCPEvaluationAdapterCandidateIsExactBoundedAndIdempotent(t *testing.T) {
	snapshot := mcpRevisionSnapshot{ServerID: "server-1", Name: "orders",
		RuntimeRef:   pkgobjectstore.Reference{URI: "object://runtime/one", SHA256: "hash"},
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
	snapshot := mcpRevisionSnapshot{ServerID: "server-1", Name: "orders",
		RuntimeRef:   pkgobjectstore.Reference{URI: "object://runtime/one", SHA256: "hash"},
		EnabledTools: []string{"lookup"}, TimeoutMS: 1000, ToolSchemaHash: "old"}
	revisions := publishedMCPRevisions(t, snapshot)
	runtime := &fakeMCPRuntime{tools: []*mcpdomain.Tool{{Name: "lookup", InputSchema: map[string]any{"type": "string"}}}}
	adapter := mcpEvaluationAdapter{revisions: revisions, runtime: runtime, runtimeStore: &fakeMCPRuntimeStore{
		values: map[string]any{"object://runtime/one": mcpRuntimeConfigEnvelope{TenantID: "tenant-1",
			Config: &mcpdomain.ServerConfig{ID: "server-1", Timeout: time.Second}}},
	}}
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
	ref := pkgobjectstore.Reference{URI: "object://runtime/original", SHA256: "runtime-hash"}
	snapshot := mcpRevisionSnapshot{ServerID: "server-1", Name: "orders", RuntimeRef: ref,
		EnabledTools: []string{"lookup"}, TimeoutMS: 1000, ToolSchemaHash: hash}
	runtime := &fakeMCPRuntime{config: &mcpdomain.ServerConfig{
		ID: "server-1", URL: "https://updated.example/mcp", Timeout: time.Second,
	}, tools: tools, callResult: map[string]any{"private": "not persisted"}}
	store := &fakeMCPRuntimeStore{values: map[string]any{ref.URI: mcpRuntimeConfigEnvelope{
		TenantID: "tenant-1", Config: &mcpdomain.ServerConfig{
			ID: "server-1", Name: "orders", URL: "https://original.example/mcp", Timeout: time.Second,
		},
	}}}
	adapter := mcpEvaluationAdapter{revisions: publishedMCPRevisions(t, snapshot), runtime: runtime, runtimeStore: store}
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
	if runtime.callConfig.URL != "https://original.example/mcp" {
		t.Fatalf("revision used mutable config: %+v", runtime.callConfig)
	}
}

func TestMCPEvaluationAdapterRejectsCrossTenantRuntimeReference(t *testing.T) {
	ref := pkgobjectstore.Reference{URI: "object://runtime/tenant-1", SHA256: "hash"}
	snapshot := mcpRevisionSnapshot{ServerID: "server-1", Name: "orders", RuntimeRef: ref,
		EnabledTools: []string{"lookup"}, TimeoutMS: 1000, ToolSchemaHash: "hash"}
	store := &fakeMCPRuntimeStore{values: map[string]any{
		ref.URI: mcpRuntimeConfigEnvelope{TenantID: "tenant-1",
			Config: &mcpdomain.ServerConfig{ID: "server-1", Timeout: time.Second}},
	}}
	adapter := mcpEvaluationAdapter{revisions: publishedMCPRevisions(t, snapshot), runtime: &fakeMCPRuntime{},
		runtimeStore: store}
	_, err := adapter.ExecuteRevision(context.Background(), "tenant-2", mcpRef("published-1"), evaldomain.EvalCase{
		Input: map[string]any{"tool": "lookup", "arguments": map[string]any{}},
	})
	if err == nil {
		t.Fatal("expected cross-tenant runtime reference rejection")
	}
}

func TestMCPEvaluationAdapterSummariesPassRealRevisionValidation(t *testing.T) {
	revisions := evalapp.NewRevisionService(&validatingRevisionStore{}, &validatingRevisionRepo{})
	runtime := &fakeMCPRuntime{config: &mcpdomain.ServerConfig{
		ID: "server-1", Name: "orders", Timeout: time.Second,
	}, tools: []*mcpdomain.Tool{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}}}
	adapter := mcpEvaluationAdapter{runtime: runtime, revisions: revisions, runtimeStore: &fakeMCPRuntimeStore{}}
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
	callConfig    *mcpdomain.ServerConfig
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
func (f *fakeMCPRuntime) ListToolsWithConfig(context.Context, *mcpdomain.ServerConfig) ([]*mcpdomain.Tool, error) {
	return f.tools, nil
}
func (f *fakeMCPRuntime) CallToolWithConfig(
	ctx context.Context, config *mcpdomain.ServerConfig, tool string, input any,
) (any, error) {
	f.callCount++
	f.callConfig = config
	f.callTool = tool
	f.callArguments, _ = input.(map[string]any)
	if tc, ok := postgres.FromContext(ctx); ok {
		f.tenantID = tc.TenantID
	}
	return f.callResult, f.callErr
}

type fakeMCPRuntimeStore struct {
	values    map[string]any
	lastValue any
	putCalls  int
	putErr    error
	deleted   []string
	putRefs   []pkgobjectstore.Reference
}

func (s *fakeMCPRuntimeStore) Put(_ context.Context, payload pkgobjectstore.Payload) (pkgobjectstore.Reference, error) {
	s.putCalls++
	if s.putErr != nil {
		return pkgobjectstore.Reference{}, s.putErr
	}
	s.lastValue = payload.Value
	if s.values == nil {
		s.values = map[string]any{}
	}
	uri := fmt.Sprintf("object://runtime/%s/%d", payload.TenantID, s.putCalls)
	s.values[uri] = payload.Value
	ref := pkgobjectstore.Reference{URI: uri, SHA256: "runtime-hash"}
	s.putRefs = append(s.putRefs, ref)
	return ref, nil
}
func (s *fakeMCPRuntimeStore) Get(_ context.Context, ref pkgobjectstore.Reference) ([]byte, error) {
	value, ok := s.values[ref.URI]
	if !ok {
		return nil, errors.New("not found")
	}
	return json.Marshal(value)
}
func (s *fakeMCPRuntimeStore) Delete(_ context.Context, ref pkgobjectstore.Reference) error {
	s.deleted = append(s.deleted, ref.URI)
	delete(s.values, ref.URI)
	return nil
}

type fakeMCPRevisionService struct {
	revision     evaldomain.ResourceRevision
	payload      []byte
	found        bool
	input        evalport.CreateRevisionInput
	createErr    error
	publishCalls int
	publishErr   error
	keys         map[string]evaldomain.ResourceRevision
	fingerprints map[string]string
}

func (f *fakeMCPRevisionService) Get(context.Context, string, evaldomain.ResourceRef) (evaldomain.ResourceRevision, []byte, bool, error) {
	return f.revision, f.payload, f.found, nil
}
func (f *fakeMCPRevisionService) Create(_ context.Context, _ string, input evalport.CreateRevisionInput) (evaldomain.ResourceRevision, bool, error) {
	f.input = input
	if f.createErr != nil {
		return evaldomain.ResourceRevision{}, false, f.createErr
	}
	fingerprint, _ := canonicalHash(input.FingerprintPayload)
	if f.keys == nil {
		f.keys, f.fingerprints = map[string]evaldomain.ResourceRevision{}, map[string]string{}
	}
	if existing, ok := f.keys[input.IdempotencyKey]; ok {
		if f.fingerprints[input.IdempotencyKey] != fingerprint {
			return evaldomain.ResourceRevision{}, false, errors.New("idempotency conflict")
		}
		return existing, false, nil
	}
	if f.revision.ID == "" {
		f.revision = evaldomain.ResourceRevision{ID: "created-1", ResourceKind: input.ResourceKind, ResourceID: input.ResourceID, Status: evaldomain.RevisionStatusDraft}
	}
	f.keys[input.IdempotencyKey], f.fingerprints[input.IdempotencyKey] = f.revision, fingerprint
	return f.revision, true, nil
}
func (f *fakeMCPRevisionService) Publish(_ context.Context, _ string, ref evaldomain.ResourceRef) (evaldomain.ResourceRevision, error) {
	f.publishCalls++
	if f.publishErr != nil {
		return evaldomain.ResourceRevision{}, f.publishErr
	}
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
