package opik

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

const metadataPrefix = "opik.metadata.stratum."

func mapEvidence(trace opikTrace, spans []opikSpan) (domain.TraceEvidence, error) {
	manifest := map[string]string{}
	if raw := metadataJSON(trace.Metadata, "resource_manifest"); len(raw) > 0 {
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return domain.TraceEvidence{}, fmt.Errorf("resource manifest: %w", domain.ErrEvidenceInvalid)
		}
	}
	experiments := map[string]domain.ResourceAssignment{}
	if raw := metadataJSON(trace.Metadata, "experiment_assignments"); len(raw) > 0 {
		if err := json.Unmarshal(raw, &experiments); err != nil {
			return domain.TraceEvidence{}, fmt.Errorf("experiment assignments: %w", domain.ErrEvidenceInvalid)
		}
	}
	assignments := make(map[string]domain.ResourceAssignment, len(manifest))
	for resource, revision := range manifest {
		assignment := experiments[resource]
		assignment.RevisionID = revision
		assignments[resource] = assignment
	}
	evidence := domain.TraceEvidence{
		OpikTraceID: trace.ID, TraceID: metadataString(trace.Metadata, "trace_id"),
		ExecutionID: metadataString(trace.Metadata, "execution_id"), AgentID: metadataString(trace.Metadata, "agent_id"),
		Status: metadataString(trace.Metadata, "status"), TotalTokens: metadataInt(trace.Metadata, "total_tokens", usageTotal(trace.Usage)),
		CostUSD:           metadataFloat(trace.Metadata, "cost_usd", trace.TotalEstimatedCost),
		LatencyMs:         metadataInt64(trace.Metadata, "duration_ms", int64(trace.Duration)),
		SecurityViolation: metadataBool(trace.Metadata, "security_violation"), ResourceAssignments: assignments,
		StartedAt: trace.StartTime,
	}
	for index, span := range spans {
		evidence.Events = append(evidence.Events, mapEvent(evidence, span, index+1))
		if span.Name == "react.tool" {
			evidence.Tools = append(evidence.Tools, mapTool(evidence, span))
		}
	}
	return evidence, nil
}

func metadataJSON(metadata map[string]any, key string) []byte {
	value, ok := metadata[metadataPrefix+key]
	if !ok {
		return nil
	}
	if typed, ok := value.(string); ok {
		return []byte(typed)
	}
	encoded, _ := json.Marshal(value)
	return encoded
}

func mapTool(evidence domain.TraceEvidence, span opikSpan) domain.ToolObservation {
	status := metadataString(span.Metadata, "status")
	if status == "" {
		status = domain.ToolTraceStatusSuccess
	}
	return domain.ToolObservation{
		ID: span.ID, TraceID: evidence.TraceID, ExecutionID: evidence.ExecutionID,
		ToolCallID: metadataString(span.Metadata, "tool_call_id"), ToolName: metadataString(span.Metadata, "tool_name"),
		ProviderType: metadataString(span.Metadata, "provider_type"), ProviderID: metadataString(span.Metadata, "provider_id"),
		ServerID: metadataString(span.Metadata, "server_id"), CapabilityID: metadataString(span.Metadata, "capability_id"),
		Status: status, ErrorMessage: errorMessage(span.ErrorInfo), LatencyMs: int64(span.Duration),
		Metadata: span.Metadata, StartedAt: span.StartTime, EndedAt: endTime(span), CreatedAt: span.StartTime,
	}
}

func mapEvent(evidence domain.TraceEvidence, span opikSpan, sequence int) domain.AgentTraceEvent {
	status := metadataString(span.Metadata, "status")
	if status == "" {
		status = domain.ToolTraceStatusSuccess
	}
	return domain.AgentTraceEvent{
		ID: span.ID, TraceID: evidence.TraceID, ExecutionID: evidence.ExecutionID, RunType: domain.RunTypeAgent,
		ObservationType: observationType(span.Name), EventType: eventType(span.Name, status), SpanName: span.Name,
		ParentEventID: span.ParentSpanID, Status: status, Input: span.Input, Output: span.Output,
		ErrorMessage: errorMessage(span.ErrorInfo), Model: span.Model, PromptTokens: usageInput(span.Usage),
		CompletionTokens: usageOutput(span.Usage), TotalTokens: usageTotal(span.Usage), CostUSD: span.TotalEstimatedCost,
		LatencyMs: int64(span.Duration), ProviderType: metadataString(span.Metadata, "provider_type"),
		ProviderID: metadataString(span.Metadata, "provider_id"), SequenceNo: int64(sequence), Metadata: span.Metadata,
		OTelSpanID: span.ID, StartedAt: span.StartTime, EndedAt: endTime(span), CreatedAt: span.StartTime,
	}
}

func metadataString(metadata map[string]any, key string) string {
	value, ok := metadata[metadataPrefix+key]
	if !ok {
		return ""
	}
	if typed, ok := value.(string); ok {
		return typed
	}
	return fmt.Sprint(value)
}

func metadataBool(metadata map[string]any, key string) bool {
	parsed, _ := strconv.ParseBool(metadataString(metadata, key))
	return parsed
}

func metadataInt(metadata map[string]any, key string, fallback int) int {
	return int(metadataInt64(metadata, key, int64(fallback)))
}

func metadataInt64(metadata map[string]any, key string, fallback int64) int64 {
	value := metadataString(metadata, key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func metadataFloat(metadata map[string]any, key string, fallback float64) float64 {
	value := metadataString(metadata, key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func usageInput(usage map[string]int) int {
	return firstUsage(usage, "input_tokens", "prompt_tokens")
}

func usageOutput(usage map[string]int) int {
	return firstUsage(usage, "output_tokens", "completion_tokens")
}

func usageTotal(usage map[string]int) int {
	if total := firstUsage(usage, "total_tokens"); total != 0 {
		return total
	}
	return usageInput(usage) + usageOutput(usage)
}

func firstUsage(usage map[string]int, keys ...string) int {
	for _, key := range keys {
		if usage[key] != 0 {
			return usage[key]
		}
	}
	return 0
}

func endTime(span opikSpan) time.Time {
	if span.EndTime != nil {
		return *span.EndTime
	}
	return span.StartTime
}

func errorMessage(info *errorInfo) string {
	if info == nil {
		return ""
	}
	return info.Message
}

func observationType(name string) string {
	if strings.Contains(name, "tool") {
		return domain.ObservationTypeTool
	}
	if strings.Contains(name, "llm") {
		return domain.ObservationTypeLLM
	}
	return domain.ObservationTypeAgent
}

func eventType(name, status string) string {
	if strings.Contains(name, "tool") {
		if status == domain.ToolTraceStatusError {
			return domain.TraceEventToolFailed
		}
		return domain.TraceEventToolFinished
	}
	if strings.Contains(name, "llm") {
		return domain.TraceEventLLMResponse
	}
	if status == domain.ToolTraceStatusError {
		return domain.TraceEventAgentFailed
	}
	return domain.TraceEventAgentFinished
}
