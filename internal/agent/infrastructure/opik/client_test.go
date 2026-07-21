package opik

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

func TestClientResolveBatchDeduplicatesTraceIDs(t *testing.T) {
	var traceRequests atomic.Int32
	var spanRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/private/traces":
			traceRequests.Add(1)
			writeTracePage(t, w, requestedTraceID(t, r))
		case "/v1/private/spans":
			spanRequests.Add(1)
			_, _ = w.Write([]byte(`{"total":0,"content":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL, Timeout: time.Second})
	evidence, err := client.ResolveBatch(context.Background(), "tenant-1", []string{"trace-1", "trace-1"})
	if err != nil {
		t.Fatalf("ResolveBatch() error: %v", err)
	}
	if len(evidence) != 1 || evidence["trace-1"].TraceID != "trace-1" {
		t.Fatalf("ResolveBatch() = %#v", evidence)
	}
	if traceRequests.Load() != 1 || spanRequests.Load() != 1 {
		t.Fatalf("requests = traces:%d spans:%d, want 1 each", traceRequests.Load(), spanRequests.Load())
	}
}

func TestClientResolveBatchBoundsConcurrency(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/private/traces" {
			current := active.Add(1)
			for current > maximum.Load() && !maximum.CompareAndSwap(maximum.Load(), current) {
			}
			time.Sleep(20 * time.Millisecond)
			active.Add(-1)
			writeTracePage(t, w, requestedTraceID(t, r))
			return
		}
		if r.URL.Path == "/v1/private/spans" {
			_, _ = w.Write([]byte(`{"total":0,"content":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	traceIDs := []string{"trace-1", "trace-2", "trace-3", "trace-4", "trace-5", "trace-6", "trace-7"}
	client := NewClient(Config{BaseURL: server.URL, Timeout: time.Second})
	if _, err := client.ResolveBatch(context.Background(), "tenant-1", traceIDs); err != nil {
		t.Fatalf("ResolveBatch() error: %v", err)
	}
	if got := maximum.Load(); got < 2 || got > int32(defaultBatchConcurrency) {
		t.Fatalf("maximum concurrency = %d, want 2..%d", got, defaultBatchConcurrency)
	}
}

func requestedTraceID(t *testing.T, r *http.Request) string {
	t.Helper()
	var filters []map[string]string
	if err := json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filters); err != nil {
		t.Fatalf("decode filters: %v", err)
	}
	for _, filter := range filters {
		if filter["key"] == metadataJSONPath("trace_id") {
			return filter["value"]
		}
	}
	t.Fatal("trace_id filter missing")
	return ""
}

func writeTracePage(t *testing.T, w http.ResponseWriter, traceID string) {
	t.Helper()
	response := map[string]any{"total": 1, "content": []any{map[string]any{
		"id": "opik-" + traceID, "start_time": "2026-07-20T01:00:00Z",
		"metadata": map[string]string{
			"opik.metadata.stratum.tenant_id": "tenant-1",
			"opik.metadata.stratum.trace_id":  traceID,
		},
	}}}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func TestClientResolveMapsTraceAndSpans(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("projectName") != "stratum-test" || r.Header.Get("Comet-Workspace") != "workspace-test" {
			t.Error("missing Opik scope headers")
		}
		switch r.URL.Path {
		case "/api/v1/private/traces":
			if !strings.Contains(r.URL.Query().Get("filters"), "business-trace-1") {
				t.Errorf("trace filter = %q", r.URL.Query().Get("filters"))
			}
			_, _ = w.Write([]byte(`{"page":1,"size":1,"total":1,"content":[{"id":"opik-trace-1","name":"agent.execute","start_time":"2026-07-20T01:00:00Z","end_time":"2026-07-20T01:00:02Z","duration":2000,"total_estimated_cost":0.12,"usage":{"total_tokens":30},"metadata":{"opik.metadata.stratum.tenant_id":"tenant-1","opik.metadata.stratum.trace_id":"business-trace-1","opik.metadata.stratum.execution_id":"execution-1","opik.metadata.stratum.agent_id":"agent-1","opik.metadata.stratum.status":"success","opik.metadata.stratum.resource_manifest":"{\"skill:skill-1\":\"revision-1\"}","opik.metadata.stratum.experiment_assignments":"{\"skill:skill-1\":{\"experiment_id\":\"experiment-1\",\"variant\":\"canary\"}}"}}]}`))
		case "/api/v1/private/spans":
			if r.URL.Query().Get("trace_id") != "opik-trace-1" {
				t.Errorf("span trace_id = %q", r.URL.Query().Get("trace_id"))
			}
			_, _ = w.Write([]byte(`{"page":1,"size":100,"total":1,"content":[{"id":"opik-span-1","trace_id":"opik-trace-1","name":"react.tool","start_time":"2026-07-20T01:00:01Z","end_time":"2026-07-20T01:00:01.250Z","duration":250,"metadata":{"opik.metadata.stratum.tenant_id":"tenant-1","opik.metadata.stratum.tool_call_id":"call-1","opik.metadata.stratum.tool_name":"search","opik.metadata.stratum.provider_type":"skill","opik.metadata.stratum.provider_id":"skill-1","opik.metadata.stratum.resource_revision_id":"revision-1","opik.metadata.stratum.status":"success"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL + "/api", Project: "stratum-test", Workspace: "workspace-test", Timeout: time.Second})
	evidence, err := client.Resolve(context.Background(), "tenant-1", "business-trace-1")
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if evidence.ExecutionID != "execution-1" || evidence.TotalTokens != 30 || evidence.CostUSD != 0.12 {
		t.Fatalf("unexpected evidence: %#v", evidence)
	}
	assignment := evidence.ResourceAssignments["skill:skill-1"]
	if assignment.RevisionID != "revision-1" || assignment.ExperimentID != "experiment-1" || assignment.Variant != "canary" {
		t.Fatalf("unexpected assignment: %#v", assignment)
	}
	if len(evidence.Tools) != 1 || evidence.Tools[0].ToolCallID != "call-1" || evidence.Tools[0].LatencyMs != 250 {
		t.Fatalf("unexpected tools: %#v", evidence.Tools)
	}
}

func TestTraceFiltersUseOpikMetadataJSONPaths(t *testing.T) {
	var filters []map[string]string
	if err := json.Unmarshal([]byte(traceFilters("tenant-1", "trace-1")), &filters); err != nil {
		t.Fatalf("decode filters: %v", err)
	}
	if len(filters) != 2 {
		t.Fatalf("filters = %#v", filters)
	}
	if filters[0]["key"] != `$['opik.metadata.stratum.tenant_id']` ||
		filters[1]["key"] != `$['opik.metadata.stratum.trace_id']` {
		t.Fatalf("metadata filter keys = %q, %q", filters[0]["key"], filters[1]["key"])
	}
}

func TestMapEvidenceAcceptsStructuredOpikMetadata(t *testing.T) {
	evidence, err := mapEvidence(opikTrace{Metadata: map[string]any{
		metadataPrefix + "total_tokens":      33,
		metadataPrefix + "cost_usd":          0.42,
		metadataPrefix + "duration_ms":       1250,
		metadataPrefix + "resource_manifest": map[string]any{"skill:skill-1": "revision-1"},
		metadataPrefix + "experiment_assignments": map[string]any{
			"skill:skill-1": map[string]any{"experiment_id": "experiment-1", "variant": "canary"},
		},
	}}, nil)
	if err != nil {
		t.Fatalf("mapEvidence() error: %v", err)
	}
	if evidence.TotalTokens != 33 || evidence.CostUSD != 0.42 || evidence.LatencyMs != 1250 {
		t.Fatalf("metrics = tokens:%d cost:%v latency:%d", evidence.TotalTokens, evidence.CostUSD, evidence.LatencyMs)
	}
	assignment := evidence.ResourceAssignments["skill:skill-1"]
	if assignment.RevisionID != "revision-1" || assignment.ExperimentID != "experiment-1" || assignment.Variant != "canary" {
		t.Fatalf("assignment = %#v", assignment)
	}
}

func TestClientResolveRejectsCrossTenantTrace(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"total":1,"content":[{"id":"opik-trace-1","start_time":"2026-07-20T01:00:00Z","metadata":{"opik.metadata.stratum.tenant_id":"tenant-2","opik.metadata.stratum.trace_id":"business-trace-1"}}]}`))
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL, Timeout: time.Second})
	_, err := client.Resolve(context.Background(), "tenant-1", "business-trace-1")
	if err == nil || !strings.Contains(err.Error(), domain.ErrEvidenceNotFound.Error()) {
		t.Fatalf("Resolve() error = %v", err)
	}
}

func TestClientResolveMapsUnavailableAndInvalidResponses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{name: "unavailable", status: http.StatusServiceUnavailable, body: `{}`, want: domain.ErrEvidenceUnavailable},
		{name: "invalid", status: http.StatusOK, body: `{`, want: domain.ErrEvidenceInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			client := NewClient(Config{BaseURL: server.URL, Timeout: time.Second})
			_, err := client.Resolve(context.Background(), "tenant-1", "trace-1")
			if err == nil || !strings.Contains(err.Error(), tt.want.Error()) {
				t.Fatalf("Resolve() error = %v, want %v", err, tt.want)
			}
		})
	}
}
