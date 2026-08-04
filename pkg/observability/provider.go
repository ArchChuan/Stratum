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
	IncSystemAssistantRequest(roleClass, profileVersion, outcome string)
	RecordSystemAssistantTTFT(roleClass, profileVersion string, duration float64)
	RecordOfficialDocsSearchResults(profileVersion, outcome string, count int)
	RecordSystemAssistantDiagnosticArea(roleClass, area, outcome string, duration float64)
	RecordSystemAssistantEvidenceGaps(roleClass, profileVersion string, count int)
	IncResourceProposal(kind, operation, outcome string)
	RecordResourceProposalReviewDuration(kind, operation string, duration float64)
	RecordResourceProposalDraftEdits(kind, operation string, count int)

	// Platform MCP uses only bounded operational labels.
	IncPlatformMCPRequest(toolClass, riskLevel, outcome string)
	RecordPlatformMCPRequestDuration(toolClass, outcome string, duration float64)
	IncPlatformMCPRequestsInFlight()
	DecPlatformMCPRequestsInFlight()
	IncPlatformMCPAuthDenial(statusClass string)
	IncPlatformMCPTokenExchange(outcome string)
	IncPlatformMCPReplayDenial(statusClass string)
	IncPlatformMCPBackendRequest(toolClass, statusClass string)
	IncPlatformMCPUnknownOutcome(toolClass string)
	IncPlatformMCPContractMismatch(toolClass string)
	SetPlatformMCPCertificateExpiry(seconds float64)
	SetPlatformMCPCertificateRotation(statusClass string, value float64)

	// LLM
	IncLLMRequest(model, provider, status string)
	RecordLLMRequestDuration(model, provider string, duration float64)
	IncLLMTokenUsage(model, tokenType string, count int64)
	RecordLLMTokenHistogram(model, tokenType string, count float64)
	RecordLLMFirstTokenLatency(model, provider string, latency float64)

	// Knowledge / Memory
	IncKnowledgeQuery(queryType, status string)
	RecordKnowledgeQueryDuration(queryType string, duration float64)
	RecordMemoryRetrievalDuration(operation string, duration float64)
	IncKnowledgeIngest(status string)
	RecordKnowledgeIngestDuration(duration float64)
	IncKnowledgeIngestInFlight()
	DecKnowledgeIngestInFlight()

	// Hermes
	IncHermesEvent(eventType string)
	IncHermesEventProcessed(eventType, status string)

	// Agent KPI (F3)
	IncAgentTaskCompleted(agentID, agentType, taskKind, outcome string)
	RecordAgentTaskLatency(agentID, taskKind string, seconds float64)
	RecordAgentCostPerTask(agentID, taskKind string, costUSD float64)
	RecordAgentEvalScore(agentID, metric string, score float64)
	RecordAgentConversationTurn(agentID string, turnCount int)

	// Scheduler (F3)
	IncScheduledFire(scheduleType, status string)

	// Reranker (F3)
	IncRerankRequest(model, status string)
	RecordRerankDuration(model string, seconds float64)

	// Model Router (F3)
	IncRouteFallback(fromModel, toModel string)
	RecordBudgetRatio(scope string, pct float64)

	// Audit (F3)
	IncAuditEvent(risk, outcome string)
	RecordAuditWriteQueueDepth(depth int)

	// Collab (F3)
	IncCollabPlan(strategy, outcome string)
	RecordCollabTaskDuration(strategy string, seconds float64)

	// Optimizer (F3)
	IncOptimizerCandidate(strategy, outcome string)
	RecordOptimizerCycleDuration(seconds float64)

	// Operation Gate (F3)
	IncOperationProposal(kind, outcome string)
	RecordApprovalLatency(kind string, seconds float64)

	// Schedule skew (F3)
	RecordScheduleSkew(skewSeconds float64)

	// Reaper
	IncReaperCycle(outcome string)
	SetReaperCycleTimestamp(ts float64)
	IncReaperGuestDeleted()
	IncReaperDeleteError(phase string)

	// Background components (generic ticker-based components)
	RecordComponentCycle(component string)
	SetComponentCycleTimestamp(component string, ts float64)
	IncComponentError(component, phase string)

	// Goroutine panic recovery
	IncGoroutinePanic(component string)

	// Workflow
	IncWorkflowRun(tenantID, status string)
	RecordWorkflowRunDuration(tenantID string, duration float64)

	// MCP internal client (backend→MCP server calls)
	IncMCPClientRequest(serverName, operation, status string)
	IncMCPClientReconnect(serverName string)

	// Evaluation
	IncEvaluationJob(status string)

	// Auth
	IncAuthFailure(reason string)
}

// NoopMetrics satisfies MetricsProvider with no-ops. Safe for tests and disabled mode.
type NoopMetrics struct{}

func (NoopMetrics) IncHTTPRequest(_, _ string, _ int)                             {}
func (NoopMetrics) RecordHTTPRequestDuration(_, _ string, _ float64)              {}
func (NoopMetrics) IncHTTPRequestsInFlight()                                      {}
func (NoopMetrics) DecHTTPRequestsInFlight()                                      {}
func (NoopMetrics) IncSkillExecution(_, _, _ string)                              {}
func (NoopMetrics) RecordSkillExecutionDuration(_ string, _ float64)              {}
func (NoopMetrics) SetSkillCircuitBreakerState(_ string, _ float64)               {}
func (NoopMetrics) IncAgentExecution(_, _, _ string)                              {}
func (NoopMetrics) RecordAgentExecutionDuration(_, _ string, _ float64)           {}
func (NoopMetrics) RecordAgentStepCount(_, _ string, _ int)                       {}
func (NoopMetrics) IncSystemAssistantRequest(_, _, _ string)                      {}
func (NoopMetrics) RecordSystemAssistantTTFT(_, _ string, _ float64)              {}
func (NoopMetrics) RecordOfficialDocsSearchResults(_, _ string, _ int)            {}
func (NoopMetrics) RecordSystemAssistantDiagnosticArea(_, _, _ string, _ float64) {}
func (NoopMetrics) RecordSystemAssistantEvidenceGaps(_, _ string, _ int)          {}
func (NoopMetrics) IncResourceProposal(_, _, _ string)                            {}
func (NoopMetrics) RecordResourceProposalReviewDuration(_, _ string, _ float64)   {}
func (NoopMetrics) RecordResourceProposalDraftEdits(_, _ string, _ int)           {}
func (NoopMetrics) IncPlatformMCPRequest(_, _, _ string)                          {}
func (NoopMetrics) RecordPlatformMCPRequestDuration(_, _ string, _ float64)       {}
func (NoopMetrics) IncPlatformMCPRequestsInFlight()                               {}
func (NoopMetrics) DecPlatformMCPRequestsInFlight()                               {}
func (NoopMetrics) IncPlatformMCPAuthDenial(_ string)                             {}
func (NoopMetrics) IncPlatformMCPTokenExchange(_ string)                          {}
func (NoopMetrics) IncPlatformMCPReplayDenial(_ string)                           {}
func (NoopMetrics) IncPlatformMCPBackendRequest(_, _ string)                      {}
func (NoopMetrics) IncPlatformMCPUnknownOutcome(_ string)                         {}
func (NoopMetrics) IncPlatformMCPContractMismatch(_ string)                       {}
func (NoopMetrics) SetPlatformMCPCertificateExpiry(_ float64)                     {}
func (NoopMetrics) SetPlatformMCPCertificateRotation(_ string, _ float64)         {}
func (NoopMetrics) IncLLMRequest(_, _, _ string)                                  {}
func (NoopMetrics) RecordLLMRequestDuration(_, _ string, _ float64)               {}
func (NoopMetrics) IncLLMTokenUsage(_, _ string, _ int64)                         {}
func (NoopMetrics) RecordLLMTokenHistogram(_, _ string, _ float64)                {}
func (NoopMetrics) RecordLLMFirstTokenLatency(_, _ string, _ float64)             {}
func (NoopMetrics) IncKnowledgeQuery(_, _ string)                                 {}
func (NoopMetrics) RecordKnowledgeQueryDuration(_ string, _ float64)              {}
func (NoopMetrics) RecordMemoryRetrievalDuration(_ string, _ float64)             {}
func (NoopMetrics) IncKnowledgeIngest(_ string)                                   {}
func (NoopMetrics) RecordKnowledgeIngestDuration(_ float64)                       {}
func (NoopMetrics) IncKnowledgeIngestInFlight()                                   {}
func (NoopMetrics) DecKnowledgeIngestInFlight()                                   {}
func (NoopMetrics) IncHermesEvent(_ string)                                       {}
func (NoopMetrics) IncHermesEventProcessed(_, _ string)                           {}
func (NoopMetrics) IncAgentTaskCompleted(_, _, _, _ string)                       {}
func (NoopMetrics) RecordAgentTaskLatency(_, _ string, _ float64)                 {}
func (NoopMetrics) RecordAgentCostPerTask(_, _ string, _ float64)                 {}
func (NoopMetrics) RecordAgentEvalScore(_, _ string, _ float64)                   {}
func (NoopMetrics) RecordAgentConversationTurn(_ string, _ int)                   {}
func (NoopMetrics) IncScheduledFire(_, _ string)                                  {}
func (NoopMetrics) IncRerankRequest(_, _ string)                                  {}
func (NoopMetrics) RecordRerankDuration(_ string, _ float64)                      {}
func (NoopMetrics) IncRouteFallback(_, _ string)                                  {}
func (NoopMetrics) RecordBudgetRatio(_ string, _ float64)                         {}
func (NoopMetrics) IncAuditEvent(_, _ string)                                     {}
func (NoopMetrics) RecordAuditWriteQueueDepth(_ int)                              {}
func (NoopMetrics) IncCollabPlan(_, _ string)                                     {}
func (NoopMetrics) RecordCollabTaskDuration(_ string, _ float64)                  {}
func (NoopMetrics) IncOptimizerCandidate(_, _ string)                             {}
func (NoopMetrics) RecordOptimizerCycleDuration(_ float64)                        {}
func (NoopMetrics) IncOperationProposal(_, _ string)                              {}
func (NoopMetrics) RecordApprovalLatency(_ string, _ float64)                     {}
func (NoopMetrics) RecordScheduleSkew(_ float64)                                  {}
func (NoopMetrics) IncReaperCycle(_ string)                                       {}
func (NoopMetrics) SetReaperCycleTimestamp(_ float64)                             {}
func (NoopMetrics) IncReaperGuestDeleted()                                        {}
func (NoopMetrics) IncReaperDeleteError(_ string)                                 {}
func (NoopMetrics) RecordComponentCycle(_ string)                                 {}
func (NoopMetrics) SetComponentCycleTimestamp(_ string, _ float64)                {}
func (NoopMetrics) IncComponentError(_, _ string)                                 {}
func (NoopMetrics) IncGoroutinePanic(_ string)                                    {}
func (NoopMetrics) IncWorkflowRun(_, _ string)                                    {}
func (NoopMetrics) RecordWorkflowRunDuration(_ string, _ float64)                 {}
func (NoopMetrics) IncMCPClientRequest(_, _, _ string)                            {}
func (NoopMetrics) IncMCPClientReconnect(_ string)                                {}
func (NoopMetrics) IncEvaluationJob(_ string)                                     {}
func (NoopMetrics) IncAuthFailure(_ string)                                       {}
