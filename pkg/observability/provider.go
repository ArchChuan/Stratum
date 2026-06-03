// Package observability provides monitoring and tracing.

package observability

// MetricsProvider is the pluggable interface for all observability metrics.
// PrometheusMetrics implements this; NoopMetrics is used in tests.
type MetricsProvider interface {
	// HTTP
	IncHTTPRequest(method, path string, statusCode int)
	RecordHTTPRequestDuration(method, path string, duration float64)
	IncHTTPRequestsInFlight()
	DecHTTPRequestsInFlight()

	// Skill
	IncSkillExecution(skillID, skillType, status string)
	RecordSkillExecutionDuration(skillID string, duration float64)
	SetSkillCircuitBreakerState(skillID string, state float64)

	// Agent
	IncAgentExecution(agentID, agentType, status string)
	RecordAgentExecutionDuration(agentID, agentType string, duration float64)
	RecordAgentStepCount(agentID, agentType string, steps int)

	// LLM
	IncLLMRequest(model, provider, status string)
	RecordLLMRequestDuration(model, provider string, duration float64)
	IncLLMTokenUsage(model, tokenType string, count int64)
	RecordLLMTokenHistogram(model, tokenType string, count float64)
	// RecordLLMFirstTokenLatency is not in the interface until streaming completions are wired.

	// Knowledge / Memory
	IncKnowledgeQuery(queryType, status string)
	RecordKnowledgeQueryDuration(queryType string, duration float64)
	RecordMemoryRetrievalDuration(operation string, duration float64)

	// Hermes
	IncHermesEvent(eventType string)
	IncHermesEventProcessed(eventType, status string)
}

// NoopMetrics satisfies MetricsProvider with no-ops. Safe for tests and disabled mode.
type NoopMetrics struct{}

func (NoopMetrics) IncHTTPRequest(_, _ string, _ int)                   {}
func (NoopMetrics) RecordHTTPRequestDuration(_, _ string, _ float64)    {}
func (NoopMetrics) IncHTTPRequestsInFlight()                            {}
func (NoopMetrics) DecHTTPRequestsInFlight()                            {}
func (NoopMetrics) IncSkillExecution(_, _, _ string)                    {}
func (NoopMetrics) RecordSkillExecutionDuration(_ string, _ float64)    {}
func (NoopMetrics) SetSkillCircuitBreakerState(_ string, _ float64)     {}
func (NoopMetrics) IncAgentExecution(_, _, _ string)                    {}
func (NoopMetrics) RecordAgentExecutionDuration(_, _ string, _ float64) {}
func (NoopMetrics) RecordAgentStepCount(_, _ string, _ int)             {}
func (NoopMetrics) IncLLMRequest(_, _, _ string)                        {}
func (NoopMetrics) RecordLLMRequestDuration(_, _ string, _ float64)     {}
func (NoopMetrics) IncLLMTokenUsage(_, _ string, _ int64)               {}
func (NoopMetrics) RecordLLMTokenHistogram(_, _ string, _ float64)      {}
func (NoopMetrics) IncKnowledgeQuery(_, _ string)                       {}
func (NoopMetrics) RecordKnowledgeQueryDuration(_ string, _ float64)    {}
func (NoopMetrics) RecordMemoryRetrievalDuration(_ string, _ float64)   {}
func (NoopMetrics) IncHermesEvent(_ string)                             {}
func (NoopMetrics) IncHermesEventProcessed(_, _ string)                 {}
