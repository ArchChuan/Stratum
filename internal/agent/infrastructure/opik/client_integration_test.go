//go:build integration

package opik

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

func TestRealOpikCollectorEvidenceParity(t *testing.T) {
	if os.Getenv("TEST_OPIK_E2E") != "1" {
		t.Skip("set TEST_OPIK_E2E=1 to run against the real Collector and Opik stack")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	otlpEndpoint := os.Getenv("TEST_OTLP_GRPC_ENDPOINT")
	if otlpEndpoint == "" {
		otlpEndpoint = "127.0.0.1:4317"
	}
	opikURL := os.Getenv("TEST_OPIK_URL")
	if opikURL == "" {
		opikURL = "http://127.0.0.1:5173/api"
	}
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(otlpEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		t.Fatalf("create OTLP exporter: %v", err)
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter,
		sdktrace.WithBatchTimeout(100*time.Millisecond)))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	businessTraceID := "opik-e2e-" + time.Now().UTC().Format("20060102T150405.000000000")
	failureTraceID := businessTraceID + "-failure"
	stableTraceID := businessTraceID + "-stable"
	tracer := provider.Tracer("stratum.opik.e2e")
	rootCtx, root := tracer.Start(ctx, "agent.execute", trace.WithAttributes(
		attribute.String("stratum.evaluation", "true"),
		attribute.String("stratum.security_violation", "true"),
		attribute.String("opik.metadata.stratum.tenant_id", "tenant-e2e"),
		attribute.String("opik.metadata.stratum.trace_id", businessTraceID),
		attribute.String("opik.metadata.stratum.execution_id", "execution-e2e"),
		attribute.String("opik.metadata.stratum.agent_id", "agent-e2e"),
		attribute.String("opik.metadata.stratum.status", "success"),
		attribute.String("opik.metadata.stratum.evaluation", "true"),
		attribute.String("opik.metadata.stratum.security_violation", "true"),
		attribute.String("opik.metadata.stratum.resource_manifest", `{"skill:skill-e2e":"revision-e2e"}`),
		attribute.String("opik.metadata.stratum.experiment_assignments", `{"skill:skill-e2e":{"experiment_id":"experiment-e2e","variant":"canary"}}`),
		attribute.Int64("opik.metadata.stratum.duration_ms", 1250),
		attribute.Int64("opik.metadata.stratum.total_tokens", 33),
		attribute.Float64("opik.metadata.stratum.cost_usd", 0.42),
	))
	graphCtx, graph := tracer.Start(rootCtx, "react.graph.invoke")
	_, llm := tracer.Start(graphCtx, "react.llm", trace.WithAttributes(
		attribute.String("opik.metadata.stratum.tenant_id", "tenant-e2e"),
		attribute.String("opik.metadata.stratum.trace_id", businessTraceID),
		attribute.String("opik.metadata.stratum.provider_type", "llm"),
		attribute.String("opik.metadata.stratum.provider_id", "model-e2e"),
		attribute.String("opik.metadata.stratum.status", "success"),
		attribute.Int("gen_ai.usage.input_tokens", 20),
		attribute.Int("gen_ai.usage.output_tokens", 13),
		attribute.Float64("opik.metadata.stratum.cost_usd", 0.42),
	))
	llm.End()
	_, tool := tracer.Start(graphCtx, "react.tool", trace.WithAttributes(
		attribute.String("opik.metadata.stratum.tenant_id", "tenant-e2e"),
		attribute.String("opik.metadata.stratum.trace_id", businessTraceID),
		attribute.String("opik.metadata.stratum.tool_call_id", "call-e2e"),
		attribute.String("opik.metadata.stratum.tool_name", "search"),
		attribute.String("opik.metadata.stratum.provider_type", "skill"),
		attribute.String("opik.metadata.stratum.provider_id", "skill-e2e"),
		attribute.String("opik.metadata.stratum.resource_revision_id", "revision-e2e"),
		attribute.String("opik.metadata.stratum.status", "success"),
	))
	tool.End()
	graph.End()
	root.SetStatus(codes.Ok, "")
	root.End()
	_, failedRoot := tracer.Start(ctx, "agent.execute", trace.WithAttributes(
		attribute.String("opik.metadata.stratum.tenant_id", "tenant-e2e"),
		attribute.String("opik.metadata.stratum.trace_id", failureTraceID),
		attribute.String("opik.metadata.stratum.execution_id", "execution-e2e-failure"),
		attribute.String("opik.metadata.stratum.agent_id", "agent-e2e"),
		attribute.String("opik.metadata.stratum.status", "error"),
		attribute.Int64("opik.metadata.stratum.duration_ms", 90),
	))
	failedRoot.SetStatus(codes.Error, "expected e2e failure")
	failedRoot.End()
	_, stableRoot := tracer.Start(ctx, "agent.execute", trace.WithAttributes(
		attribute.String("stratum.experiment.id", "experiment-e2e"),
		attribute.String("opik.metadata.stratum.tenant_id", "tenant-e2e"),
		attribute.String("opik.metadata.stratum.trace_id", stableTraceID),
		attribute.String("opik.metadata.stratum.execution_id", "execution-e2e-stable"),
		attribute.String("opik.metadata.stratum.agent_id", "agent-e2e"),
		attribute.String("opik.metadata.stratum.status", "success"),
		attribute.String("opik.metadata.stratum.resource_manifest", `{"skill:skill-e2e":"revision-stable"}`),
		attribute.String("opik.metadata.stratum.experiment_assignments", `{"skill:skill-e2e":{"experiment_id":"experiment-e2e","variant":"stable"}}`),
		attribute.Int64("opik.metadata.stratum.duration_ms", 100),
		attribute.Float64("opik.metadata.stratum.cost_usd", 0.2),
	))
	stableRoot.SetStatus(codes.Ok, "")
	stableRoot.End()
	if err := provider.ForceFlush(ctx); err != nil {
		t.Fatalf("flush OTLP spans: %v", err)
	}

	client := NewClient(Config{
		BaseURL: opikURL, Project: "Default Project", Timeout: 3 * time.Second,
	})
	var evidenceErr error
	var evidenceTotalTokens int
	var evidenceCost float64
	var evidenceLatency int64
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		evidence, resolveErr := client.Resolve(ctx, "tenant-e2e", businessTraceID)
		if resolveErr == nil {
			evidenceTotalTokens, evidenceCost, evidenceLatency = evidence.TotalTokens, evidence.CostUSD, evidence.LatencyMs
			if evidence.ExecutionID != "execution-e2e" || evidence.Status != "success" || !evidence.SecurityViolation {
				t.Fatalf("unexpected root evidence: %#v", evidence)
			}
			assignment := evidence.ResourceAssignments["skill:skill-e2e"]
			if assignment.RevisionID != "revision-e2e" || assignment.ExperimentID != "experiment-e2e" || assignment.Variant != "canary" {
				t.Fatalf("unexpected resource assignment: %#v", assignment)
			}
			if len(evidence.Tools) != 1 || evidence.Tools[0].ToolCallID != "call-e2e" {
				t.Fatalf("unexpected tool evidence: %#v", evidence.Tools)
			}
			if err := assertRealHierarchy(evidence.Events); err != nil {
				t.Fatal(err)
			}
			evidenceErr = nil
			break
		}
		evidenceErr = resolveErr
		time.Sleep(500 * time.Millisecond)
	}
	if evidenceErr != nil {
		t.Fatalf("resolve real Opik evidence: %v", evidenceErr)
	}
	if evidenceTotalTokens != 33 || evidenceCost != 0.42 || evidenceLatency != 1250 {
		t.Fatalf("metrics = tokens:%d cost:%v latency:%d, want 33/0.42/1250",
			evidenceTotalTokens, evidenceCost, evidenceLatency)
	}
	if _, err := client.Resolve(ctx, "tenant-other", businessTraceID); !errors.Is(err, domain.ErrEvidenceNotFound) {
		t.Fatalf("cross-tenant Resolve() error = %v, want evidence not found", err)
	}
	failureEvidence, err := client.Resolve(ctx, "tenant-e2e", failureTraceID)
	if err != nil {
		t.Fatalf("resolve retained failure evidence: %v", err)
	}
	if failureEvidence.Status != "error" || failureEvidence.ExecutionID != "execution-e2e-failure" {
		t.Fatalf("unexpected failure evidence: %#v", failureEvidence)
	}
	stableEvidence, err := client.Resolve(ctx, "tenant-e2e", stableTraceID)
	if err != nil {
		t.Fatalf("resolve retained stable evidence: %v", err)
	}
	stableAssignment := stableEvidence.ResourceAssignments["skill:skill-e2e"]
	if stableAssignment.RevisionID != "revision-stable" || stableAssignment.Variant != "stable" {
		t.Fatalf("unexpected stable assignment: %#v", stableAssignment)
	}
}

func assertRealHierarchy(events []domain.AgentTraceEvent) error {
	ids := make(map[string]string, len(events))
	for _, event := range events {
		ids[event.SpanName] = event.ID
	}
	graphID := ids["react.graph.invoke"]
	if graphID == "" {
		return errors.New("react.graph.invoke event missing")
	}
	for _, event := range events {
		if (event.SpanName == "react.llm" || event.SpanName == "react.tool") && event.ParentEventID != graphID {
			return errors.New("react child is not parented by react.graph.invoke")
		}
	}
	return nil
}
