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
	agentExecutionsTotal   *prometheus.CounterVec
	agentExecutionDuration *prometheus.HistogramVec
	agentStepCount         *prometheus.HistogramVec

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

	// Hermes
	hermesEventsTotal     *prometheus.CounterVec
	hermesEventsProcessed *prometheus.CounterVec

	logger *zap.Logger
}

// NewPrometheusMetrics registers all metrics and returns a ready MetricsProvider.
func NewPrometheusMetrics(logger *zap.Logger) *PrometheusMetrics {
	tokenBuckets := []float64{64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384}
	latencyBuckets := []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60}

	// Use a private registry so multiple instances (e.g. in tests) don't conflict.
	reg := prometheus.NewRegistry()
	factory := promauto.With(reg)

	return &PrometheusMetrics{
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
}

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
