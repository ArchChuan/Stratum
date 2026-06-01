package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

// PrometheusMetrics 封装 Prometheus 指标
type PrometheusMetrics struct {
	// HTTP 指标
	httpRequestsTotal     *prometheus.CounterVec
	httpRequestDuration   *prometheus.HistogramVec
	httpRequestsInFlight  prometheus.Gauge

	// Skill 执行指标
	skillExecutionsTotal    *prometheus.CounterVec
	skillExecutionDuration  *prometheus.HistogramVec
	skillCircuitBreakerState *prometheus.GaugeVec

	// Agent 执行指标
	agentExecutionsTotal  *prometheus.CounterVec
	agentExecutionDuration *prometheus.HistogramVec

	// LLM 调用指标
	llmRequestsTotal      *prometheus.CounterVec
	llmRequestDuration    *prometheus.HistogramVec
	llmTokenUsage         *prometheus.CounterVec

	// 知识库指标
	knowledgeQueriesTotal *prometheus.CounterVec
	knowledgeQueryDuration *prometheus.HistogramVec

	// Hermes 事件指标
	hermesEventsTotal     *prometheus.CounterVec
	hermesEventsProcessed *prometheus.CounterVec

	logger *zap.Logger
}

// NewPrometheusMetrics 创建新的 Prometheus 指标
func NewPrometheusMetrics(logger *zap.Logger) *PrometheusMetrics {
	return &PrometheusMetrics{
		// HTTP 指标
		httpRequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total number of HTTP requests",
			},
			[]string{"method", "path", "status"},
		),
		httpRequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_duration_seconds",
				Help:    "HTTP request duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "path"},
		),
		httpRequestsInFlight: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "http_requests_in_flight",
				Help: "Number of HTTP requests currently in flight",
			},
		),

		// Skill 执行指标
		skillExecutionsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "skill_executions_total",
				Help: "Total number of skill executions",
			},
			[]string{"skill_id", "skill_type", "status"},
		),
		skillExecutionDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "skill_execution_duration_seconds",
				Help:    "Skill execution duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"skill_id"},
		),
		skillCircuitBreakerState: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "skill_circuit_breaker_state",
				Help: "Circuit breaker state per skill (0=closed, 1=open, 2=half_open)",
			},
			[]string{"skill_id"},
		),

		// Agent 执行指标
		agentExecutionsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_executions_total",
				Help: "Total number of agent executions",
			},
			[]string{"agent_id", "agent_type", "status"},
		),
		agentExecutionDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "agent_execution_duration_seconds",
				Help:    "Agent execution duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"agent_id", "agent_type"},
		),

		// LLM 调用指标
		llmRequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "llm_requests_total",
				Help: "Total number of LLM requests",
			},
			[]string{"model", "provider", "status"},
		),
		llmRequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "llm_request_duration_seconds",
				Help:    "LLM request duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"model", "provider"},
		),
		llmTokenUsage: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "llm_token_usage_total",
				Help: "Total number of LLM tokens used",
			},
			[]string{"model", "type"},
		),

		// 知识库指标
		knowledgeQueriesTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "knowledge_queries_total",
				Help: "Total number of knowledge queries",
			},
			[]string{"query_type", "status"},
		),
		knowledgeQueryDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "knowledge_query_duration_seconds",
				Help:    "Knowledge query duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"query_type"},
		),

		// Hermes 事件指标
		hermesEventsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "hermes_events_total",
				Help: "Total number of Hermes events published",
			},
			[]string{"event_type"},
		),
		hermesEventsProcessed: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "hermes_events_processed_total",
				Help: "Total number of Hermes events processed",
			},
			[]string{"event_type", "status"},
		),

		logger: logger,
	}
}

// HTTP 指标方法

// IncHTTPRequest 增加HTTP请求计数
func (m *PrometheusMetrics) IncHTTPRequest(method, path string, statusCode int) {
	status := httpStatusCategory(statusCode)
	m.httpRequestsTotal.WithLabelValues(method, path, status).Inc()
}

// RecordHTTPRequestDuration 记录HTTP请求持续时间
func (m *PrometheusMetrics) RecordHTTPRequestDuration(method, path string, duration float64) {
	m.httpRequestDuration.WithLabelValues(method, path).Observe(duration)
}

// IncHTTPRequestsInFlight 增加正在处理的HTTP请求
func (m *PrometheusMetrics) IncHTTPRequestsInFlight() {
	m.httpRequestsInFlight.Inc()
}

// DecHTTPRequestsInFlight 减少正在处理的HTTP请求
func (m *PrometheusMetrics) DecHTTPRequestsInFlight() {
	m.httpRequestsInFlight.Dec()
}

// Skill 指标方法

// IncSkillExecution 增加技能执行计数
func (m *PrometheusMetrics) IncSkillExecution(skillID, skillType, status string) {
	m.skillExecutionsTotal.WithLabelValues(skillID, skillType, status).Inc()
}

// SetSkillCircuitBreakerState 设置熔断器状态 gauge
func (m *PrometheusMetrics) SetSkillCircuitBreakerState(skillID string, state float64) {
	m.skillCircuitBreakerState.WithLabelValues(skillID).Set(state)
}

// RecordSkillExecutionDuration 记录技能执行持续时间
func (m *PrometheusMetrics) RecordSkillExecutionDuration(skillID string, duration float64) {
	m.skillExecutionDuration.WithLabelValues(skillID).Observe(duration)
}

// Agent 指标方法

// IncAgentExecution 增加代理执行计数
func (m *PrometheusMetrics) IncAgentExecution(agentID, agentType, status string) {
	m.agentExecutionsTotal.WithLabelValues(agentID, agentType, status).Inc()
}

// RecordAgentExecutionDuration 记录代理执行持续时间
func (m *PrometheusMetrics) RecordAgentExecutionDuration(agentID, agentType string, duration float64) {
	m.agentExecutionDuration.WithLabelValues(agentID, agentType).Observe(duration)
}

// LLM 指标方法

// IncLLMRequest 增加LLM请求计数
func (m *PrometheusMetrics) IncLLMRequest(model, provider, status string) {
	m.llmRequestsTotal.WithLabelValues(model, provider, status).Inc()
}

// RecordLLMRequestDuration 记录LLM请求持续时间
func (m *PrometheusMetrics) RecordLLMRequestDuration(model, provider string, duration float64) {
	m.llmRequestDuration.WithLabelValues(model, provider).Observe(duration)
}

// IncLLMTokenUsage 增加LLM token使用计数
func (m *PrometheusMetrics) IncLLMTokenUsage(model, tokenType string, count int64) {
	m.llmTokenUsage.WithLabelValues(model, tokenType).Add(float64(count))
}

// 知识库指标方法

// IncKnowledgeQuery 增加知识库查询计数
func (m *PrometheusMetrics) IncKnowledgeQuery(queryType, status string) {
	m.knowledgeQueriesTotal.WithLabelValues(queryType, status).Inc()
}

// RecordKnowledgeQueryDuration 记录知识库查询持续时间
func (m *PrometheusMetrics) RecordKnowledgeQueryDuration(queryType string, duration float64) {
	m.knowledgeQueryDuration.WithLabelValues(queryType).Observe(duration)
}

// Hermes 事件指标方法

// IncHermesEvent 增加Hermes事件计数
func (m *PrometheusMetrics) IncHermesEvent(eventType string) {
	m.hermesEventsTotal.WithLabelValues(eventType).Inc()
}

// IncHermesEventProcessed 增加Hermes事件处理计数
func (m *PrometheusMetrics) IncHermesEventProcessed(eventType, status string) {
	m.hermesEventsProcessed.WithLabelValues(eventType, status).Inc()
}

// GetHandler 返回 Prometheus metrics handler
func (m *PrometheusMetrics) GetHandler() http.Handler {
	return promhttp.Handler()
}

// httpStatusCategory 返回 HTTP 状态码分类
func httpStatusCategory(statusCode int) string {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return "2xx"
	case statusCode >= 300 && statusCode < 400:
		return "3xx"
	case statusCode >= 400 && statusCode < 500:
		return "4xx"
	case statusCode >= 500:
		return "5xx"
	default:
		return "unknown"
	}
}