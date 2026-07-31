package observability

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestSystemAssistantMetricsUseOnlyBoundedLabels(t *testing.T) {
	m := NewPrometheusMetrics(zap.NewNop())
	m.IncSystemAssistantRequest("member", "2026-07-23.v1", "success")
	m.RecordSystemAssistantTTFT("member", "2026-07-23.v1", 0.1)
	m.RecordOfficialDocsSearchResults("2026-07-23.v1", "matched", 2)
	m.RecordSystemAssistantDiagnosticArea("admin", "mcp", "unavailable", 0.2)
	m.RecordSystemAssistantEvidenceGaps("admin", "2026-07-23.v1", 1)
	m.IncResourceProposal("agent", "update", "applied")
	m.RecordResourceProposalReviewDuration("agent", "update", 2)
	m.RecordResourceProposalDraftEdits("agent", "update", 3)
	families, err := m.reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if !strings.HasPrefix(family.GetName(), "system_assistant_") {
			continue
		}
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				switch label.GetName() {
				case "role_class", "profile_version", "outcome", "area", "kind", "operation":
				default:
					t.Fatalf("unbounded assistant metric label %q on %s", label.GetName(), family.GetName())
				}
			}
		}
	}
}

func TestPlatformMCPMetricsUseOnlyBoundedLabels(t *testing.T) {
	m := NewPrometheusMetrics(zap.NewNop())
	m.InitPlatformMCPMetrics()
	m.IncPlatformMCPRequest("diagnostic", "read", "success")
	m.RecordPlatformMCPRequestDuration("diagnostic", "success", 0.1)
	m.IncPlatformMCPRequestsInFlight()
	m.DecPlatformMCPRequestsInFlight()
	m.IncPlatformMCPAuthDenial("4xx")
	m.IncPlatformMCPTokenExchange("success")
	m.IncPlatformMCPReplayDenial("4xx")
	m.IncPlatformMCPBackendRequest("diagnostic", "2xx")
	m.IncPlatformMCPUnknownOutcome("proposal")
	m.IncPlatformMCPContractMismatch("proposal")
	m.SetPlatformMCPCertificateExpiry(86400)
	m.SetPlatformMCPCertificateRotation("success", 1)

	families, err := m.reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"tool_class": true, "risk_level": true, "outcome": true, "status_class": true}
	for _, family := range families {
		if !strings.HasPrefix(family.GetName(), "platform_mcp_") {
			continue
		}
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if !allowed[label.GetName()] {
					t.Fatalf("unbounded Platform MCP label %q on %s", label.GetName(), family.GetName())
				}
			}
		}
	}
}

func TestResourceProposalDraftEditsRecordsOneObservation(t *testing.T) {
	m := NewPrometheusMetrics(zap.NewNop())
	m.RecordResourceProposalDraftEdits("knowledge_workspace", "update", 2)
	families, err := m.reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "system_assistant_resource_proposal_draft_edits" {
			continue
		}
		if len(family.Metric) != 1 || family.Metric[0].GetHistogram().GetSampleCount() != 1 {
			t.Fatalf("draft edit histogram = %#v", family.Metric)
		}
		return
	}
	t.Fatal("draft edit histogram not registered")
}

func TestDefaultTraceConfig(t *testing.T) {
	cfg := DefaultTraceConfig()
	if cfg == nil {
		t.Fatal("expected config to be non-nil")
	}
	if cfg.ServiceName == "" {
		t.Error("expected non-empty ServiceName")
	}
}

func TestNoopMetricsImplementsProvider(t *testing.T) {
	var _ MetricsProvider = NoopMetrics{}
}

func TestPrometheusMetricsImplementsProvider(t *testing.T) {
	logger := zap.NewNop()
	var _ MetricsProvider = NewPrometheusMetrics(logger)
}

func TestLogTracerStartSpan(t *testing.T) {
	logger := zap.NewNop()
	tracer := NewTracer(logger)

	ctx := context.Background()
	childCtx, span := tracer.StartSpan(ctx, "test.operation")
	if span == nil {
		t.Fatal("expected non-nil span")
	}

	sc, ok := SpanFromContext(childCtx)
	if !ok {
		t.Fatal("expected SpanContext in child context")
	}
	if sc.TraceID == "" {
		t.Error("expected non-empty TraceID")
	}
	if sc.SpanID == "" {
		t.Error("expected non-empty SpanID")
	}
	if sc.Name != "test.operation" {
		t.Errorf("expected span name 'test.operation', got %q", sc.Name)
	}

	span.SetAttribute("key", "value")
	span.End()
}

func TestLogTracerPropagatesTraceID(t *testing.T) {
	logger := zap.NewNop()
	tracer := NewTracer(logger)

	ctx := context.Background()
	ctx1, span1 := tracer.StartSpan(ctx, "parent")
	sc1, _ := SpanFromContext(ctx1)
	span1.End()

	ctx2, span2 := tracer.StartSpan(ctx1, "child")
	sc2, _ := SpanFromContext(ctx2)
	span2.End()

	if sc1.TraceID != sc2.TraceID {
		t.Errorf("child should inherit parent TraceID: parent=%s child=%s", sc1.TraceID, sc2.TraceID)
	}
	if sc1.SpanID == sc2.SpanID {
		t.Error("parent and child should have distinct SpanIDs")
	}
}

func TestNoopTracerProvider(t *testing.T) {
	cfg := &TraceConfig{ExporterType: "none"}
	p := NewTraceProvider(cfg, zap.NewNop())

	ctx, span := p.StartSpan(context.Background(), "op")
	span.End()
	span.SetAttribute("x", 1)
	span.RecordError(nil)

	_, ok := SpanFromContext(ctx)
	if ok {
		t.Error("noopTracer should not set SpanContext")
	}
}

func TestPrometheusMetricsLLM(t *testing.T) {
	logger := zap.NewNop()
	m := NewPrometheusMetrics(logger)

	m.IncLLMRequest("claude-3-sonnet", "claude", "success")
	m.RecordLLMRequestDuration("claude-3-sonnet", "claude", 1.5)
	m.IncLLMTokenUsage("claude-3-sonnet", "prompt", 512)
	m.RecordLLMTokenHistogram("claude-3-sonnet", "completion", 256)
	// RecordLLMFirstTokenLatency is available on *PrometheusMetrics directly (not in interface yet)
	m.RecordLLMFirstTokenLatency("claude-3-sonnet", "claude", 0.3)
}

func TestPrometheusMetricsAgent(t *testing.T) {
	logger := zap.NewNop()
	m := NewPrometheusMetrics(logger)

	m.IncAgentExecution("agent-1", "react", "success")
	m.RecordAgentExecutionDuration("agent-1", "react", 2.0)
	m.RecordAgentStepCount("agent-1", "react", 5)
}
