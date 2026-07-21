# Agent OTel Evaluation Observability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Stratum's OTel Agent traces preserve correct parentage, carry safe evaluation and resource-version evidence, and retain evaluation, experiment, failure, and security traces at 100% sampling.

**Architecture:** Keep PostgreSQL trace/evaluation records unchanged while enriching the existing OTel path. Execution metadata enters through `ExecMeta`/`ExecutionConfig`, experiment assignment returns revision plus variant evidence, Agent/ReAct/LLM spans use a shared safe-attribute helper, and Collector tail sampling selects protected traces by root-span attributes or error status.

**Tech Stack:** Go 1.22+, OpenTelemetry Go SDK, OTel Collector tail sampling, testify, in-memory OTel span recorder, Docker Compose Collector/Jaeger E2E.

---

## Task 1: Define safe Agent observation attributes

**Files:**

- Create: `internal/agent/application/telemetry.go`
- Test: `internal/agent/application/telemetry_test.go`

- [ ] Write failing tests proving previews are UTF-8 safe, bounded, redact sensitive keys, and hashes remain stable for the original value.
- [ ] Run `go test ./internal/agent/application -run 'TestTelemetry' -count=1` and confirm the tests fail because the helpers do not exist.
- [ ] Implement focused helpers that produce preview/hash attributes without exporting raw secrets. Use existing trace byte limits where applicable and never emit API keys.
- [ ] Re-run the focused tests and confirm they pass.

Expected helper shape:

```go
type tracePayload struct {
    Preview   string
    SHA256    string
    Truncated bool
}

func safeTracePayload(value any, maxRunes int) tracePayload
func executionAttributes(agentID string, agentType domain.AgentType, cfg ExecutionConfig, manifest map[string]string) []attribute.KeyValue
```

## Task 2: Preserve execution metadata and span hierarchy

**Files:**

- Modify: `internal/agent/application/agent.go`
- Modify: `internal/agent/application/agent_service.go`
- Test: `internal/agent/application/react_agent_test.go`
- Test: `internal/agent/application/agent_service_test.go`

- [ ] Add failing in-memory SpanRecorder tests asserting `react.graph.invoke` is a child of `agent.execute`, while `react.llm` and `react.tool` are children of `react.graph.invoke`.
- [ ] Add failing tests asserting the root span contains tenant, user, execution, business trace, conversation, evaluation, experiment, variant, and resource-manifest attributes.
- [ ] Add a failing service test proving `assembleOptions` passes the generated execution ID into `ExecutionConfig`.
- [ ] Run the focused tests and confirm failures show the old context and missing attributes.
- [ ] Extend `ExecMeta` and `ExecutionConfig` with typed observability metadata:

```go
type EvolutionTraceMetadata struct {
    Evaluation       bool
    ExperimentID     string
    Variant          string
    ResourceManifest map[string]string
}
```

- [ ] Add `WithEvolutionTraceMetadata`, pass `WithExecutionID(executionID)`, and attach safe root attributes.
- [ ] Use returned contexts for memory/history, ReAct graph, planning graph, and chat persistence spans. Ensure every span is ended on all paths.
- [ ] Re-run focused tests and confirm correct parent IDs and root attributes.

## Task 3: Enrich LLM and tool spans and mark failures correctly

**Files:**

- Modify: `internal/agent/application/graph/react.go`
- Modify: `internal/llmgateway/infrastructure/gateway.go`
- Test: `internal/agent/application/graph/react_test.go`
- Test: `internal/llmgateway/infrastructure/gateway_test.go`

- [ ] Add failing span-recorder tests for LLM model, step, token usage, cost, safe input/output preview/hash, and tool-call presence.
- [ ] Add failing tests for tool call ID/name/provider/server/capability/revision, argument/result preview/hash, and truncation attributes.
- [ ] Add failing tests proving LLM/tool/gateway failures set OTel status to `codes.Error`, not only exception events.
- [ ] Run focused tests and confirm the new assertions fail.
- [ ] Add the minimal attributes at the existing span creation/completion points; reuse safe payload helpers rather than writing full raw payloads.
- [ ] Propagate returned tool span contexts where nested capability spans need them.
- [ ] Call both `RecordError` and `SetStatus(codes.Error, safeMessage)` on failures.
- [ ] Re-run focused tests and confirm they pass.

## Task 4: Propagate experiment assignment evidence

**Files:**

- Modify: `internal/agent/domain/port/capability.go`
- Modify: `internal/agent/application/agent_service.go`
- Modify: `internal/evaluation/application/experiment_service.go`
- Modify: `api/wiring/evaluation.go`
- Test: `internal/agent/application/agent_service_test.go`
- Test: `internal/evaluation/application/experiment_service_test.go`
- Test: `api/wiring/evaluation_test.go`

- [ ] Add failing tests for a typed skill assignment containing revision ID, experiment ID, and `stable`/`canary` variant.
- [ ] Add failing tests proving an evaluation run sets `Evaluation=true` and includes its tested resource revision in the root resource manifest.
- [ ] Run focused tests and confirm current revision-only resolution cannot satisfy them.
- [ ] Introduce a backward-compatible assignment result and have `ExperimentService` resolve deployment plus selected variant deterministically.
- [ ] Aggregate resolved Skill revisions into `ResourceManifest` and attach experiment metadata when a live assignment exists.
- [ ] Mark evaluation-worker executions explicitly in `api/wiring/evaluation.go`.
- [ ] Re-run focused tests and confirm assignment evidence reaches execution metadata.

## Task 5: Protect evaluation, experiment, security, and failed traces from sampling

**Files:**

- Modify: `otel-collector-config.yaml`
- Modify: `k8s/tracing.yaml`
- Test: `pkg/observability/collector_config_test.go`

- [ ] Add failing YAML contract tests that require 100% attribute policies for `stratum.evaluation=true`, non-empty `stratum.experiment.id`, and `stratum.security_violation=true`, plus the existing `ERROR` policy and 10% ordinary policy.
- [ ] Run `go test ./pkg/observability -run TestCollector -count=1` and confirm it fails on missing policies.
- [ ] Add OTel tail-sampling `string_attribute` policies in both local Compose and Kubernetes Collector configurations.
- [ ] Keep ordinary traces at 10%, errors and slow traces retained, and ensure Agent failures explicitly set status ERROR.
- [ ] Re-run configuration tests and confirm both deployment variants pass.

## Task 6: Regression and real OTLP verification

**Files:**

- Create temporarily, then delete: `internal/agent/application/tmp_agent_otel_validation_test.go`
- Modify only if evidence reveals a defect: files from Tasks 1-5

- [ ] Run focused package tests:

```bash
go test ./pkg/observability ./internal/agent/application ./internal/agent/application/graph ./internal/llmgateway/infrastructure ./internal/evaluation/application ./api/wiring -count=1
```

- [ ] Run repository short tests:

```bash
go vet ./...
go test -short ./... -count=1
```

- [ ] Start isolated PostgreSQL/Redis/NATS/Milvus/Collector/Jaeger services and the backend using test configuration.
- [ ] Execute a synthetic two-step Agent run with one MCP-classified tool through the real OTLP exporter.
- [ ] Inspect Collector detailed output and verify span count, parent IDs, required safe attributes, and ERROR/evaluation/experiment sampling behavior.
- [ ] Confirm no token, API key, password, or raw secret appears in exported attributes.
- [ ] Delete temporary validation files, restore temporary Collector verbosity, stop the backend, remove containers and volumes, and verify ports are closed.
- [ ] Run `git diff --check` and `git status --short`; report whether PostgreSQL trace tables can be removed based on the measured OTel evidence coverage.
