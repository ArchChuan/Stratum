// Package observability provides monitoring and tracing.

package observability

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

// PrometheusMetrics implements MetricsProvider using Prometheus counters/histograms.
type PrometheusMetrics struct {
	reg *prometheus.Registry

	// HTTP
	httpRequestsTotal    *prometheus.CounterVec
	httpRequestDuration  *prometheus.HistogramVec
	httpRequestsInFlight prometheus.Gauge

	// Skill
	skillExecutionsTotal     *prometheus.CounterVec
	skillExecutionDuration   *prometheus.HistogramVec
	skillCircuitBreakerState *prometheus.GaugeVec

	// Agent
	agentExecutionsTotal              *prometheus.CounterVec
	agentExecutionDuration            *prometheus.HistogramVec
	agentStepCount                    *prometheus.HistogramVec
	systemAssistantRequests           *prometheus.CounterVec
	systemAssistantTTFT               *prometheus.HistogramVec
	systemAssistantSearchResults      *prometheus.HistogramVec
	systemAssistantDiagnosticDuration *prometheus.HistogramVec
	systemAssistantEvidenceGaps       *prometheus.HistogramVec
	resourceProposalsTotal            *prometheus.CounterVec
	resourceProposalReviewDuration    *prometheus.HistogramVec
	resourceProposalDraftEdits        *prometheus.HistogramVec
	platformMCP                       *platformMCPMetrics

	// LLM – core
	llmRequestsTotal   *prometheus.CounterVec
	llmRequestDuration *prometheus.HistogramVec
	llmTokenUsage      *prometheus.CounterVec
	// LLM – AI-specific
	llmTokenHistogram    *prometheus.HistogramVec
	llmFirstTokenLatency *prometheus.HistogramVec

	// Knowledge / Memory
	knowledgeQueriesTotal   *prometheus.CounterVec
	knowledgeQueryDuration  *prometheus.HistogramVec
	memoryRetrievalDuration *prometheus.HistogramVec
	knowledgeIngestTotal    *prometheus.CounterVec
	knowledgeIngestDuration prometheus.Histogram
	knowledgeIngestInFlight prometheus.Gauge

	// Hermes
	hermesEventsTotal     *prometheus.CounterVec
	hermesEventsProcessed *prometheus.CounterVec

	// Agent KPI (F3)
	agentTaskCompletedTotal *prometheus.CounterVec
	agentTaskDuration       *prometheus.HistogramVec
	agentCostPerTask        *prometheus.HistogramVec
	agentEvalScore          *prometheus.GaugeVec
	agentConversationTurns  *prometheus.HistogramVec

	// Scheduler (F3)
	scheduledFireTotal *prometheus.CounterVec

	// Reranker (F3)
	rerankRequestTotal    *prometheus.CounterVec
	rerankDurationSeconds *prometheus.HistogramVec

	// Model Router (F3)
	routeFallbackTotal *prometheus.CounterVec
	budgetRatio        *prometheus.GaugeVec

	// Audit (F3)
	auditEventTotal      *prometheus.CounterVec
	auditWriteQueueDepth prometheus.Gauge

	// Collab (F3)
	collabPlanTotal    *prometheus.CounterVec
	collabTaskDuration *prometheus.HistogramVec

	// Optimizer (F3)
	optimizerCandidateTotal *prometheus.CounterVec
	optimizerCycleDuration  prometheus.Histogram

	// Operation Gate (F3)
	operationProposalTotal *prometheus.CounterVec
	approvalLatency        *prometheus.HistogramVec

	// Schedule skew (F3)
	scheduleSkewSeconds prometheus.Histogram

	// Reaper
	reaperCyclesTotal    *prometheus.CounterVec
	reaperGuestsDeleted  prometheus.Counter
	reaperDeleteErrors   *prometheus.CounterVec
	reaperCycleTimestamp prometheus.Gauge

	// Background components (generic)
	componentCyclesTotal    *prometheus.CounterVec
	componentCycleTimestamp *prometheus.GaugeVec
	componentErrorsTotal    *prometheus.CounterVec

	// Goroutine panics
	goroutinePanicsTotal *prometheus.CounterVec

	// Workflow
	workflowRunsTotal   *prometheus.CounterVec
	workflowRunDuration *prometheus.HistogramVec

	// MCP internal client
	mcpClientRequestsTotal   *prometheus.CounterVec
	mcpClientReconnectsTotal *prometheus.CounterVec

	// Evaluation
	evaluationJobsTotal *prometheus.CounterVec

	// Auth
	authFailuresTotal *prometheus.CounterVec

	logger *zap.Logger
}

type platformMCPMetrics struct {
	requests            *prometheus.CounterVec
	requestDuration     *prometheus.HistogramVec
	requestsInFlight    prometheus.Gauge
	authDenials         *prometheus.CounterVec
	tokenExchanges      *prometheus.CounterVec
	replayDenials       *prometheus.CounterVec
	backendRequests     *prometheus.CounterVec
	unknownOutcomes     *prometheus.CounterVec
	contractMismatches  *prometheus.CounterVec
	certificateExpiry   prometheus.Gauge
	certificateRotation *prometheus.GaugeVec
}

func newPlatformMCPMetrics(factory promauto.Factory, latencyBuckets []float64) platformMCPMetrics {
	return platformMCPMetrics{
		requests: factory.NewCounterVec(
			prometheus.CounterOpts{Name: "platform_mcp_requests_total", Help: "Platform MCP requests by bounded class and outcome"},
			[]string{"tool_class", "risk_level", "outcome"},
		),
		requestDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{Name: "platform_mcp_request_duration_seconds", Help: "Platform MCP request latency", Buckets: latencyBuckets},
			[]string{"tool_class", "outcome"},
		),
		requestsInFlight: factory.NewGauge(
			prometheus.GaugeOpts{Name: "platform_mcp_requests_in_flight", Help: "Platform MCP requests in flight"},
		),
		authDenials: factory.NewCounterVec(
			prometheus.CounterOpts{Name: "platform_mcp_auth_denials_total", Help: "Platform MCP authorization denials"},
			[]string{"status_class"},
		),
		tokenExchanges: factory.NewCounterVec(
			prometheus.CounterOpts{Name: "platform_mcp_token_exchanges_total", Help: "Platform MCP token exchanges"},
			[]string{"outcome"},
		),
		replayDenials: factory.NewCounterVec(
			prometheus.CounterOpts{Name: "platform_mcp_replay_denials_total", Help: "Platform MCP replay denials"},
			[]string{"status_class"},
		),
		backendRequests: factory.NewCounterVec(
			prometheus.CounterOpts{Name: "platform_mcp_backend_requests_total", Help: "Platform MCP backend request outcomes"},
			[]string{"tool_class", "status_class"},
		),
		unknownOutcomes: factory.NewCounterVec(
			prometheus.CounterOpts{Name: "platform_mcp_unknown_outcomes_total", Help: "Platform MCP unknown outcomes"},
			[]string{"tool_class"},
		),
		contractMismatches: factory.NewCounterVec(
			prometheus.CounterOpts{Name: "platform_mcp_contract_mismatches_total", Help: "Platform MCP contract mismatches"},
			[]string{"tool_class"},
		),
		certificateExpiry: factory.NewGauge(
			prometheus.GaugeOpts{Name: "platform_mcp_certificate_expiry_seconds", Help: "Seconds until Platform MCP certificate expiry"},
		),
		certificateRotation: factory.NewGaugeVec(
			prometheus.GaugeOpts{Name: "platform_mcp_certificate_rotation", Help: "Latest certificate rotation status"},
			[]string{"status_class"},
		),
	}
}

var (
	tokenBuckets   = []float64{64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384}
	latencyBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60}
)

// NewPrometheusMetrics registers all metrics and returns a ready MetricsProvider.
func NewPrometheusMetrics(logger *zap.Logger) *PrometheusMetrics {
	// Use a private registry so multiple instances (e.g. in tests) don't conflict.
	reg := prometheus.NewRegistry()
	factory := promauto.With(reg)

	m := &PrometheusMetrics{
		reg: reg,
		// HTTP
		httpRequestsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{Name: "http_requests_total", Help: "Total HTTP requests"},
			[]string{"method", "path", "status"},
		),
		httpRequestDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{Name: "http_request_duration_seconds", Help: "HTTP request duration", Buckets: prometheus.DefBuckets},
			[]string{"method", "path"},
		),
		httpRequestsInFlight: factory.NewGauge(
			prometheus.GaugeOpts{Name: "http_requests_in_flight", Help: "In-flight HTTP requests"},
		),

		// Skill
		skillExecutionsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{Name: "skill_executions_total", Help: "Total skill executions"},
			[]string{"skill_id", "skill_type", "status"},
		),
		skillExecutionDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{Name: "skill_execution_duration_seconds", Help: "Skill execution duration", Buckets: prometheus.DefBuckets},
			[]string{"skill_id"},
		),
		skillCircuitBreakerState: factory.NewGaugeVec(
			prometheus.GaugeOpts{Name: "skill_circuit_breaker_state", Help: "Circuit breaker state (0=closed,1=open,2=half_open)"},
			[]string{"skill_id"},
		),

		// Agent
		agentExecutionsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{Name: "agent_executions_total", Help: "Total agent executions"},
			[]string{"agent_id", "agent_type", "status"},
		),
		agentExecutionDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{Name: "agent_execution_duration_seconds", Help: "Agent execution duration", Buckets: latencyBuckets},
			[]string{"agent_id", "agent_type"},
		),
		agentStepCount: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "agent_step_count",
				Help:    "Number of reasoning steps per agent execution",
				Buckets: []float64{1, 2, 3, 5, 8, 13, 21, 34},
			},
			[]string{"agent_id", "agent_type"},
		),
		systemAssistantRequests: factory.NewCounterVec(
			prometheus.CounterOpts{Name: "system_assistant_requests_total", Help: "System assistant requests by bounded role and outcome"},
			[]string{"role_class", "profile_version", "outcome"},
		),
		systemAssistantTTFT: factory.NewHistogramVec(
			prometheus.HistogramOpts{Name: "system_assistant_ttft_seconds", Help: "System assistant time to first token", Buckets: latencyBuckets},
			[]string{"role_class", "profile_version"},
		),
		systemAssistantSearchResults: factory.NewHistogramVec(
			prometheus.HistogramOpts{Name: "system_assistant_official_search_results", Help: "Official document search result count", Buckets: []float64{0, 1, 2, 3, 5}},
			[]string{"profile_version", "outcome"},
		),
		systemAssistantDiagnosticDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{Name: "system_assistant_diagnostic_area_duration_seconds", Help: "Diagnostic area duration", Buckets: latencyBuckets},
			[]string{"role_class", "area", "outcome"},
		),
		systemAssistantEvidenceGaps: factory.NewHistogramVec(
			prometheus.HistogramOpts{Name: "system_assistant_evidence_gaps", Help: "Evidence gap count", Buckets: []float64{0, 1, 2, 3, 5}},
			[]string{"role_class", "profile_version"},
		),
		resourceProposalsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{Name: "system_assistant_resource_proposals_total", Help: "Resource proposal outcomes by bounded kind and operation"},
			[]string{"kind", "operation", "outcome"},
		),
		resourceProposalReviewDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{Name: "system_assistant_resource_proposal_review_duration_seconds", Help: "Resource proposal review duration", Buckets: latencyBuckets},
			[]string{"kind", "operation"},
		),
		resourceProposalDraftEdits: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "system_assistant_resource_proposal_draft_edits",
				Help:    "Draft edit count observed when a resource proposal is claimed",
				Buckets: []float64{0, 1, 2, 3, 5, 8},
			},
			[]string{"kind", "operation"},
		),
		platformMCP: nil, // initialized via InitPlatformMCPMetrics

		// LLM – core
		llmRequestsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{Name: "llm_requests_total", Help: "Total LLM requests"},
			[]string{"model", "provider", "status"},
		),
		llmRequestDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{Name: "llm_request_duration_seconds", Help: "LLM request duration", Buckets: latencyBuckets},
			[]string{"model", "provider"},
		),
		llmTokenUsage: factory.NewCounterVec(
			prometheus.CounterOpts{Name: "llm_token_usage_total", Help: "Cumulative LLM tokens used"},
			[]string{"model", "type"},
		),

		// LLM – AI-specific
		llmTokenHistogram: factory.NewHistogramVec(
			prometheus.HistogramOpts{Name: "llm_token_count", Help: "Token count distribution per LLM call", Buckets: tokenBuckets},
			[]string{"model", "type"},
		),
		llmFirstTokenLatency: factory.NewHistogramVec(
			prometheus.HistogramOpts{Name: "llm_first_token_latency_seconds", Help: "Time to first token (TTFT)", Buckets: prometheus.DefBuckets},
			[]string{"model", "provider"},
		),

		// Knowledge / Memory
		knowledgeQueriesTotal: factory.NewCounterVec(
			prometheus.CounterOpts{Name: "knowledge_queries_total", Help: "Total knowledge queries"},
			[]string{"query_type", "status"},
		),
		knowledgeQueryDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{Name: "knowledge_query_duration_seconds", Help: "Knowledge query duration", Buckets: prometheus.DefBuckets},
			[]string{"query_type"},
		),
		memoryRetrievalDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{Name: "memory_retrieval_duration_seconds", Help: "Memory retrieval/storage duration", Buckets: prometheus.DefBuckets},
			[]string{"operation"},
		),
		knowledgeIngestTotal: factory.NewCounterVec(
			prometheus.CounterOpts{Name: "knowledge_ingest_total", Help: "Total knowledge ingest jobs by terminal status"},
			[]string{"status"},
		),
		knowledgeIngestDuration: factory.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "knowledge_ingest_duration_seconds",
				Help:    "Wall-clock duration of a knowledge ingest job (chunking + embed + persist)",
				Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600},
			},
		),
		knowledgeIngestInFlight: factory.NewGauge(
			prometheus.GaugeOpts{Name: "knowledge_ingest_in_flight", Help: "In-flight knowledge ingest jobs"},
		),

		// Hermes
		hermesEventsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{Name: "hermes_events_total", Help: "Total Hermes events published"},
			[]string{"event_type"},
		),
		hermesEventsProcessed: factory.NewCounterVec(
			prometheus.CounterOpts{Name: "hermes_events_processed_total", Help: "Total Hermes events processed"},
			[]string{"event_type", "status"},
		),

		logger: logger,
	}
	m.registerF3Metrics(factory, latencyBuckets)
	m.registerExtendedMetrics(factory)
	m.registerReaperMetrics(factory)
	return m
}

// registerReaperMetrics registers the reaper metric family. Must not be
// inlined into registerExtendedMetrics: the reaper is a background component
// with its own alerting rules (see helm stratum-prometheusrule.yaml).
//
// Upstream main already registers the same family (same fqName, help and
// label names) inside registerExtendedMetrics. Re-registering an identical
// descriptor from a second collector would trip the registry's duplicate
// collector check (checkCollectorID runs before the descID check), so skip
// when the fields are already populated by the upstream registration.
func (m *PrometheusMetrics) registerReaperMetrics(factory promauto.Factory) {
	if m.reaperCyclesTotal != nil {
		return
	}
	m.reaperCyclesTotal = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "reaper_cycles_total", Help: "Guest reaper cycles by outcome"},
		[]string{"outcome"},
	)
	m.reaperGuestsDeleted = factory.NewCounter(
		prometheus.CounterOpts{Name: "reaper_guests_deleted_total", Help: "Expired guests deleted by the guest reaper"},
	)
	m.reaperDeleteErrors = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "reaper_delete_errors_total", Help: "Guest reaper delete errors by phase"},
		[]string{"phase"},
	)
	m.reaperCycleTimestamp = factory.NewGauge(
		prometheus.GaugeOpts{Name: "reaper_last_cycle_timestamp_seconds", Help: "Unix timestamp of the last guest reaper cycle"},
	)
}

// registerF3Metrics initializes the Phase 1 KPI / observability metrics.
func (m *PrometheusMetrics) registerF3Metrics(factory promauto.Factory, latencyBuckets []float64) {
	m.agentTaskCompletedTotal = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "agent_task_completed_total", Help: "Agent tasks completed by type and outcome"},
		[]string{"agent_id", "agent_type", "task_kind", "outcome", "tenant_id"},
	)
	m.agentTaskDuration = factory.NewHistogramVec(
		prometheus.HistogramOpts{Name: "agent_task_duration_seconds", Help: "Agent task wall-clock duration", Buckets: []float64{0.5, 1, 2, 5, 10, 30, 60, 120, 300}},
		[]string{"agent_id", "task_kind"},
	)
	m.agentCostPerTask = factory.NewHistogramVec(
		prometheus.HistogramOpts{Name: "agent_cost_per_task_usd", Help: "Agent task cost in USD", Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5}},
		[]string{"agent_id", "task_kind"},
	)
	m.agentEvalScore = factory.NewGaugeVec(
		prometheus.GaugeOpts{Name: "agent_eval_score", Help: "Latest agent evaluation score per metric"},
		[]string{"agent_id", "metric"},
	)
	m.agentConversationTurns = factory.NewHistogramVec(
		prometheus.HistogramOpts{Name: "agent_conversation_turns", Help: "Conversation turn count per execution", Buckets: []float64{1, 2, 3, 5, 8, 13, 21, 34}},
		[]string{"agent_id"},
	)
	m.scheduledFireTotal = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "scheduled_fire_total", Help: "Schedule fires by type and status"},
		[]string{"schedule_type", "status"},
	)
	m.rerankRequestTotal = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "rerank_request_total", Help: "Rerank requests by model and status"},
		[]string{"model", "status"},
	)
	m.rerankDurationSeconds = factory.NewHistogramVec(
		prometheus.HistogramOpts{Name: "rerank_duration_seconds", Help: "Rerank request duration", Buckets: latencyBuckets},
		[]string{"model"},
	)
	m.routeFallbackTotal = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "route_fallback_total", Help: "Model route fallback events"},
		[]string{"from_model", "to_model"},
	)
	m.budgetRatio = factory.NewGaugeVec(
		prometheus.GaugeOpts{Name: "budget_ratio", Help: "Current budget consumption ratio"},
		[]string{"scope"},
	)
	m.auditEventTotal = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "audit_event_total", Help: "Audit events by risk level and outcome"},
		[]string{"risk", "outcome"},
	)
	m.auditWriteQueueDepth = factory.NewGauge(
		prometheus.GaugeOpts{Name: "audit_write_queue_depth", Help: "Current audit write buffer queue depth"},
	)
	m.collabPlanTotal = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "collab_plan_total", Help: "Collaboration plans created by strategy"},
		[]string{"strategy"},
	)
	m.collabTaskDuration = factory.NewHistogramVec(
		prometheus.HistogramOpts{Name: "collab_task_duration_seconds", Help: "Collaboration task execution duration", Buckets: latencyBuckets},
		[]string{"strategy"},
	)
	m.optimizerCandidateTotal = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "optimizer_candidate_total", Help: "Optimization candidates by strategy and outcome"},
		[]string{"strategy", "outcome"},
	)
	m.optimizerCycleDuration = factory.NewHistogram(
		prometheus.HistogramOpts{Name: "optimizer_cycle_duration_seconds", Help: "Optimizer cycle wall-clock duration", Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600}},
	)
	m.operationProposalTotal = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "operation_proposal_total", Help: "Operation proposals by kind and outcome"},
		[]string{"kind", "outcome"},
	)
	m.approvalLatency = factory.NewHistogramVec(
		prometheus.HistogramOpts{Name: "approval_latency_seconds", Help: "Approval decision latency", Buckets: latencyBuckets},
		[]string{"kind"},
	)
	m.scheduleSkewSeconds = factory.NewHistogram(
		prometheus.HistogramOpts{Name: "schedule_skew_seconds", Help: "Schedule fire time skew", Buckets: []float64{0.1, 0.5, 1, 5, 10, 30, 60, 300}},
	)
}

// registerExtendedMetrics registers metrics added after the initial
// implementation to keep NewPrometheusMetrics under the file-wide
// 120-line ratchet limit.
func (m *PrometheusMetrics) registerExtendedMetrics(factory promauto.Factory) {
	m.componentCyclesTotal = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "component_cycles_total", Help: "Background component cycles by outcome"},
		[]string{"component", "outcome"},
	)
	m.componentCycleTimestamp = factory.NewGaugeVec(
		prometheus.GaugeOpts{Name: "component_last_cycle_timestamp_seconds", Help: "Unix timestamp of last component cycle"},
		[]string{"component"},
	)
	m.componentErrorsTotal = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "component_errors_total", Help: "Component errors by phase"},
		[]string{"component", "phase"},
	)
	m.goroutinePanicsTotal = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "goroutine_panics_total", Help: "Total goroutine panics recovered"},
		[]string{"component"},
	)
	m.workflowRunsTotal = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "workflow_runs_total", Help: "Total workflow runs by status"},
		[]string{"tenant_id", "status"},
	)
	m.workflowRunDuration = factory.NewHistogramVec(
		prometheus.HistogramOpts{Name: "workflow_run_duration_seconds", Help: "Workflow run duration", Buckets: latencyBuckets},
		[]string{"tenant_id"},
	)
	m.mcpClientRequestsTotal = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "mcp_client_requests_total", Help: "Internal MCP client requests by operation and status"},
		[]string{"server_name", "operation", "status"},
	)
	m.mcpClientReconnectsTotal = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "mcp_client_reconnects_total", Help: "Internal MCP client reconnect attempts"},
		[]string{"server_name"},
	)
	m.evaluationJobsTotal = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "evaluation_jobs_total", Help: "Evaluation jobs by outcome"},
		[]string{"status"},
	)
	m.authFailuresTotal = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "auth_failures_total", Help: "Auth failures by reason"},
		[]string{"reason"},
	)
	m.reaperCyclesTotal = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "reaper_cycles_total", Help: "Guest reaper cycles by outcome"},
		[]string{"outcome"},
	)
	m.reaperGuestsDeleted = factory.NewCounter(
		prometheus.CounterOpts{Name: "reaper_guests_deleted_total", Help: "Expired guests deleted by the guest reaper"},
	)
	m.reaperDeleteErrors = factory.NewCounterVec(
		prometheus.CounterOpts{Name: "reaper_delete_errors_total", Help: "Guest reaper delete errors by phase"},
		[]string{"phase"},
	)
	m.reaperCycleTimestamp = factory.NewGauge(
		prometheus.GaugeOpts{Name: "reaper_last_cycle_timestamp_seconds", Help: "Unix timestamp of the last guest reaper cycle"},
	)
}

// Registerer returns the private prometheus.Registerer so callers (e.g. pipeline)
// can register their own metrics against the same registry.
func (m *PrometheusMetrics) Registerer() prometheus.Registerer { return m.reg }

// GetHandler returns a Prometheus scrape handler scoped to this instance's registry.
func (m *PrometheusMetrics) GetHandler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// --- HTTP ---

func (m *PrometheusMetrics) IncHTTPRequest(method, path string, statusCode int) {
	if statusCode <= 0 {
		statusCode = 200
	}
	m.httpRequestsTotal.WithLabelValues(method, path, strconv.Itoa(statusCode/100)+"xx").Inc()
}

func (m *PrometheusMetrics) RecordHTTPRequestDuration(method, path string, duration float64) {
	m.httpRequestDuration.WithLabelValues(method, path).Observe(duration)
}

func (m *PrometheusMetrics) IncHTTPRequestsInFlight() { m.httpRequestsInFlight.Inc() }
func (m *PrometheusMetrics) DecHTTPRequestsInFlight() { m.httpRequestsInFlight.Dec() }

// --- Skill ---

func (m *PrometheusMetrics) IncSkillExecution(skillID, skillType, status string) {
	m.skillExecutionsTotal.WithLabelValues(skillID, skillType, status).Inc()
}

func (m *PrometheusMetrics) RecordSkillExecutionDuration(skillID string, duration float64) {
	m.skillExecutionDuration.WithLabelValues(skillID).Observe(duration)
}

func (m *PrometheusMetrics) SetSkillCircuitBreakerState(skillID string, state float64) {
	m.skillCircuitBreakerState.WithLabelValues(skillID).Set(state)
}

// --- Agent ---

func (m *PrometheusMetrics) IncAgentExecution(agentID, agentType, status string) {
	m.agentExecutionsTotal.WithLabelValues(agentID, agentType, status).Inc()
}

func (m *PrometheusMetrics) RecordAgentExecutionDuration(agentID, agentType string, duration float64) {
	m.agentExecutionDuration.WithLabelValues(agentID, agentType).Observe(duration)
}

func (m *PrometheusMetrics) RecordAgentStepCount(agentID, agentType string, steps int) {
	m.agentStepCount.WithLabelValues(agentID, agentType).Observe(float64(steps))
}

func (m *PrometheusMetrics) IncSystemAssistantRequest(roleClass, profileVersion, outcome string) {
	m.systemAssistantRequests.WithLabelValues(roleClass, profileVersion, outcome).Inc()
}

func (m *PrometheusMetrics) RecordSystemAssistantTTFT(roleClass, profileVersion string, duration float64) {
	m.systemAssistantTTFT.WithLabelValues(roleClass, profileVersion).Observe(duration)
}

func (m *PrometheusMetrics) RecordOfficialDocsSearchResults(profileVersion, outcome string, count int) {
	m.systemAssistantSearchResults.WithLabelValues(profileVersion, outcome).Observe(float64(count))
}

func (m *PrometheusMetrics) RecordSystemAssistantDiagnosticArea(roleClass, area, outcome string, duration float64) {
	m.systemAssistantDiagnosticDuration.WithLabelValues(roleClass, area, outcome).Observe(duration)
}

func (m *PrometheusMetrics) RecordSystemAssistantEvidenceGaps(roleClass, profileVersion string, count int) {
	m.systemAssistantEvidenceGaps.WithLabelValues(roleClass, profileVersion).Observe(float64(count))
}

func (m *PrometheusMetrics) IncResourceProposal(kind, operation, outcome string) {
	m.resourceProposalsTotal.WithLabelValues(kind, operation, outcome).Inc()
}

func (m *PrometheusMetrics) RecordResourceProposalReviewDuration(kind, operation string, duration float64) {
	m.resourceProposalReviewDuration.WithLabelValues(kind, operation).Observe(duration)
}

func (m *PrometheusMetrics) RecordResourceProposalDraftEdits(kind, operation string, count int) {
	m.resourceProposalDraftEdits.WithLabelValues(kind, operation).Observe(float64(count))
}

// InitPlatformMCPMetrics registers platform MCP metrics with the provider's
// registry. Callers that do not serve the Platform MCP protocol (e.g. the main
// stratum backend) must not call this so that unset metrics do not trigger false
// Prometheus alerts.
func (m *PrometheusMetrics) InitPlatformMCPMetrics() {
	metrics := newPlatformMCPMetrics(promauto.With(m.reg), latencyBuckets)
	m.platformMCP = &metrics
}

func (m *PrometheusMetrics) IncPlatformMCPRequest(toolClass, riskLevel, outcome string) {
	if m.platformMCP == nil {
		return
	}
	m.platformMCP.requests.WithLabelValues(toolClass, riskLevel, outcome).Inc()
}

func (m *PrometheusMetrics) RecordPlatformMCPRequestDuration(toolClass, outcome string, duration float64) {
	if m.platformMCP == nil {
		return
	}
	m.platformMCP.requestDuration.WithLabelValues(toolClass, outcome).Observe(duration)
}

func (m *PrometheusMetrics) IncPlatformMCPRequestsInFlight() {
	if m.platformMCP == nil {
		return
	}
	m.platformMCP.requestsInFlight.Inc()
}
func (m *PrometheusMetrics) DecPlatformMCPRequestsInFlight() {
	if m.platformMCP == nil {
		return
	}
	m.platformMCP.requestsInFlight.Dec()
}
func (m *PrometheusMetrics) IncPlatformMCPAuthDenial(statusClass string) {
	if m.platformMCP == nil {
		return
	}
	m.platformMCP.authDenials.WithLabelValues(statusClass).Inc()
}
func (m *PrometheusMetrics) IncPlatformMCPTokenExchange(outcome string) {
	if m.platformMCP == nil {
		return
	}
	m.platformMCP.tokenExchanges.WithLabelValues(outcome).Inc()
}
func (m *PrometheusMetrics) IncPlatformMCPReplayDenial(statusClass string) {
	if m.platformMCP == nil {
		return
	}
	m.platformMCP.replayDenials.WithLabelValues(statusClass).Inc()
}
func (m *PrometheusMetrics) IncPlatformMCPBackendRequest(toolClass, statusClass string) {
	if m.platformMCP == nil {
		return
	}
	m.platformMCP.backendRequests.WithLabelValues(toolClass, statusClass).Inc()
}
func (m *PrometheusMetrics) IncPlatformMCPUnknownOutcome(toolClass string) {
	if m.platformMCP == nil {
		return
	}
	m.platformMCP.unknownOutcomes.WithLabelValues(toolClass).Inc()
}
func (m *PrometheusMetrics) IncPlatformMCPContractMismatch(toolClass string) {
	if m.platformMCP == nil {
		return
	}
	m.platformMCP.contractMismatches.WithLabelValues(toolClass).Inc()
}
func (m *PrometheusMetrics) SetPlatformMCPCertificateExpiry(seconds float64) {
	if m.platformMCP == nil {
		return
	}
	m.platformMCP.certificateExpiry.Set(seconds)
}
func (m *PrometheusMetrics) SetPlatformMCPCertificateRotation(statusClass string, value float64) {
	if m.platformMCP == nil {
		return
	}
	m.platformMCP.certificateRotation.WithLabelValues(statusClass).Set(value)
}

// --- LLM ---

func (m *PrometheusMetrics) IncLLMRequest(model, provider, status string) {
	m.llmRequestsTotal.WithLabelValues(model, provider, status).Inc()
}

func (m *PrometheusMetrics) RecordLLMRequestDuration(model, provider string, duration float64) {
	m.llmRequestDuration.WithLabelValues(model, provider).Observe(duration)
}

func (m *PrometheusMetrics) IncLLMTokenUsage(model, tokenType string, count int64) {
	m.llmTokenUsage.WithLabelValues(model, tokenType).Add(float64(count))
}

func (m *PrometheusMetrics) RecordLLMTokenHistogram(model, tokenType string, count float64) {
	m.llmTokenHistogram.WithLabelValues(model, tokenType).Observe(count)
}

func (m *PrometheusMetrics) RecordLLMFirstTokenLatency(model, provider string, latency float64) {
	m.llmFirstTokenLatency.WithLabelValues(model, provider).Observe(latency)
}

// --- Knowledge / Memory ---

func (m *PrometheusMetrics) IncKnowledgeQuery(queryType, status string) {
	m.knowledgeQueriesTotal.WithLabelValues(queryType, status).Inc()
}

func (m *PrometheusMetrics) RecordKnowledgeQueryDuration(queryType string, duration float64) {
	m.knowledgeQueryDuration.WithLabelValues(queryType).Observe(duration)
}

func (m *PrometheusMetrics) IncKnowledgeIngest(status string) {
	m.knowledgeIngestTotal.WithLabelValues(status).Inc()
}

func (m *PrometheusMetrics) RecordKnowledgeIngestDuration(duration float64) {
	m.knowledgeIngestDuration.Observe(duration)
}

func (m *PrometheusMetrics) IncKnowledgeIngestInFlight() { m.knowledgeIngestInFlight.Inc() }
func (m *PrometheusMetrics) DecKnowledgeIngestInFlight() { m.knowledgeIngestInFlight.Dec() }

func (m *PrometheusMetrics) RecordMemoryRetrievalDuration(operation string, duration float64) {
	m.memoryRetrievalDuration.WithLabelValues(operation).Observe(duration)
}

// --- Hermes ---

func (m *PrometheusMetrics) IncHermesEvent(eventType string) {
	m.hermesEventsTotal.WithLabelValues(eventType).Inc()
}

func (m *PrometheusMetrics) IncHermesEventProcessed(eventType, status string) {
	m.hermesEventsProcessed.WithLabelValues(eventType, status).Inc()
}

// --- Agent KPI (F3) ---

func (m *PrometheusMetrics) IncAgentTaskCompleted(agentID, agentType, taskKind, outcome string) {
	m.agentTaskCompletedTotal.WithLabelValues(agentID, agentType, taskKind, outcome, "").Inc()
}

func (m *PrometheusMetrics) RecordAgentTaskLatency(agentID, taskKind string, seconds float64) {
	m.agentTaskDuration.WithLabelValues(agentID, taskKind).Observe(seconds)
}

func (m *PrometheusMetrics) RecordAgentCostPerTask(agentID, taskKind string, costUSD float64) {
	m.agentCostPerTask.WithLabelValues(agentID, taskKind).Observe(costUSD)
}

func (m *PrometheusMetrics) RecordAgentEvalScore(agentID, metric string, score float64) {
	m.agentEvalScore.WithLabelValues(agentID, metric).Set(score)
}

func (m *PrometheusMetrics) RecordAgentConversationTurn(agentID string, turnCount int) {
	m.agentConversationTurns.WithLabelValues(agentID).Observe(float64(turnCount))
}

// --- Scheduler (F3) ---

func (m *PrometheusMetrics) IncScheduledFire(scheduleType, status string) {
	m.scheduledFireTotal.WithLabelValues(scheduleType, status).Inc()
}

// --- Reranker (F3) ---

func (m *PrometheusMetrics) IncRerankRequest(model, status string) {
	m.rerankRequestTotal.WithLabelValues(model, status).Inc()
}

func (m *PrometheusMetrics) RecordRerankDuration(model string, seconds float64) {
	m.rerankDurationSeconds.WithLabelValues(model).Observe(seconds)
}

// --- Model Router (F3) ---

func (m *PrometheusMetrics) IncRouteFallback(fromModel, toModel string) {
	m.routeFallbackTotal.WithLabelValues(fromModel, toModel).Inc()
}

func (m *PrometheusMetrics) RecordBudgetRatio(scope string, pct float64) {
	m.budgetRatio.WithLabelValues(scope).Set(pct)
}

// --- Audit (F3) ---

func (m *PrometheusMetrics) IncAuditEvent(risk, outcome string) {
	m.auditEventTotal.WithLabelValues(risk, outcome).Inc()
}

func (m *PrometheusMetrics) RecordAuditWriteQueueDepth(depth int) {
	m.auditWriteQueueDepth.Set(float64(depth))
}

// --- Collab (F3) ---

func (m *PrometheusMetrics) IncCollabPlan(strategy string) {
	m.collabPlanTotal.WithLabelValues(strategy).Inc()
}

func (m *PrometheusMetrics) RecordCollabTaskDuration(strategy string, seconds float64) {
	m.collabTaskDuration.WithLabelValues(strategy).Observe(seconds)
}

// --- Optimizer (F3) ---

func (m *PrometheusMetrics) IncOptimizerCandidate(strategy, outcome string) {
	m.optimizerCandidateTotal.WithLabelValues(strategy, outcome).Inc()
}

func (m *PrometheusMetrics) RecordOptimizerCycleDuration(seconds float64) {
	m.optimizerCycleDuration.Observe(seconds)
}

// --- Operation Gate (F3) ---

func (m *PrometheusMetrics) IncOperationProposal(kind, outcome string) {
	m.operationProposalTotal.WithLabelValues(kind, outcome).Inc()
}

func (m *PrometheusMetrics) RecordApprovalLatency(kind string, seconds float64) {
	m.approvalLatency.WithLabelValues(kind).Observe(seconds)
}

// --- Schedule skew (F3) ---

func (m *PrometheusMetrics) RecordScheduleSkew(skewSeconds float64) {
	m.scheduleSkewSeconds.Observe(skewSeconds)
}

// --- Reaper ---

func (m *PrometheusMetrics) IncReaperCycle(outcome string) {
	m.reaperCyclesTotal.WithLabelValues(outcome).Inc()
}

func (m *PrometheusMetrics) SetReaperCycleTimestamp(ts float64) {
	m.reaperCycleTimestamp.Set(ts)
}

func (m *PrometheusMetrics) IncReaperGuestDeleted() {
	m.reaperGuestsDeleted.Inc()
}

func (m *PrometheusMetrics) IncReaperDeleteError(phase string) {
	m.reaperDeleteErrors.WithLabelValues(phase).Inc()
}

// --- Background components (generic) ---

func (m *PrometheusMetrics) RecordComponentCycle(component string) {
	m.componentCyclesTotal.WithLabelValues(component, "ok").Inc()
}

func (m *PrometheusMetrics) SetComponentCycleTimestamp(component string, ts float64) {
	m.componentCycleTimestamp.WithLabelValues(component).Set(ts)
}

func (m *PrometheusMetrics) IncComponentError(component, phase string) {
	m.componentErrorsTotal.WithLabelValues(component, phase).Inc()
	m.componentCyclesTotal.WithLabelValues(component, "error").Inc()
}

// --- Goroutine panics ---

func (m *PrometheusMetrics) IncGoroutinePanic(component string) {
	m.goroutinePanicsTotal.WithLabelValues(component).Inc()
}

// --- Workflow ---

func (m *PrometheusMetrics) IncWorkflowRun(tenantID, status string) {
	m.workflowRunsTotal.WithLabelValues(tenantID, status).Inc()
}

func (m *PrometheusMetrics) RecordWorkflowRunDuration(tenantID string, duration float64) {
	m.workflowRunDuration.WithLabelValues(tenantID).Observe(duration)
}

// --- MCP internal client ---

func (m *PrometheusMetrics) IncMCPClientRequest(serverName, operation, status string) {
	m.mcpClientRequestsTotal.WithLabelValues(serverName, operation, status).Inc()
}

func (m *PrometheusMetrics) IncMCPClientReconnect(serverName string) {
	m.mcpClientReconnectsTotal.WithLabelValues(serverName).Inc()
}

// --- Evaluation ---

func (m *PrometheusMetrics) IncEvaluationJob(status string) {
	m.evaluationJobsTotal.WithLabelValues(status).Inc()
}

// --- Auth ---

func (m *PrometheusMetrics) IncAuthFailure(reason string) {
	m.authFailuresTotal.WithLabelValues(reason).Inc()
}
