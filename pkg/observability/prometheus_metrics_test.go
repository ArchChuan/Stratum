package observability

import (
	"testing"

	"go.uber.org/zap"
)

// exerciseAllMetrics calls every MetricsProvider method with representative
// dummy arguments. Any metric that is declared but never registered makes the
// underlying Prometheus vector nil and panics, which fails this test.
func exerciseAllMetrics(m MetricsProvider) {
	// HTTP
	m.IncHTTPRequest("GET", "/health", 200)
	m.RecordHTTPRequestDuration("GET", "/health", 0.1)
	m.IncHTTPRequestsInFlight()
	m.DecHTTPRequestsInFlight()
	// Skill
	m.IncSkillExecution("skill-1", "rag", "ok")
	m.RecordSkillExecutionDuration("skill-1", 0.1)
	m.SetSkillCircuitBreakerState("skill-1", 0)
	// Agent
	m.IncAgentExecution("agent-1", "react", "ok")
	m.RecordAgentExecutionDuration("agent-1", "react", 0.1)
	m.RecordAgentStepCount("agent-1", "react", 3)
	m.IncSystemAssistantRequest("admin", "v1", "ok")
	m.RecordSystemAssistantTTFT("admin", "v1", 0.2)
	m.RecordOfficialDocsSearchResults("v1", "ok", 2)
	m.RecordSystemAssistantDiagnosticArea("admin", "security", "ok", 0.3)
	m.RecordSystemAssistantEvidenceGaps("v1", "ok", 1)
	m.IncResourceProposal("memory", "create", "approved")
	m.RecordResourceProposalReviewDuration("memory", "create", 0.4)
	m.RecordResourceProposalDraftEdits("memory", "create", 2)
	// LLM
	m.IncLLMRequest("qwen-plus", "qwen", "ok")
	m.RecordLLMRequestDuration("qwen-plus", "qwen", 0.5)
	m.IncLLMTokenUsage("qwen-plus", "prompt", 100)
	m.RecordLLMTokenHistogram("qwen-plus", "completion", 50)
	m.RecordLLMFirstTokenLatency("qwen-plus", "qwen", 0.3)
	// Knowledge / Memory
	m.IncKnowledgeQuery("rag", "ok")
	m.RecordKnowledgeQueryDuration("rag", 0.2)
	m.RecordMemoryRetrievalDuration("search", 0.1)
	m.IncKnowledgeIngest("ok")
	m.RecordKnowledgeIngestDuration(1.5)
	m.IncKnowledgeIngestInFlight()
	m.DecKnowledgeIngestInFlight()
	// Hermes
	m.IncHermesEvent("memory.raw")
	m.IncHermesEventProcessed("memory.raw", "ok")
	// Agent KPI (F3)
	m.IncAgentTaskCompleted("agent-1", "react", "proposal", "ok")
	m.RecordAgentTaskLatency("agent-1", "proposal", 1.0)
	m.RecordAgentCostPerTask("agent-1", "proposal", 0.01)
	m.RecordAgentEvalScore("agent-1", "accuracy", 0.9)
	m.RecordAgentConversationTurn("agent-1", 5)
	// Scheduler / Reranker / Router
	m.IncScheduledFire("cron", "ok")
	m.IncRerankRequest("bge-m3", "ok")
	m.RecordRerankDuration("bge-m3", 0.2)
	m.IncRouteFallback("qwen-plus", "qwen-turbo")
	m.RecordBudgetRatio("tenant-1", 42)
	// Audit / Collab / Optimizer / Operation Gate / Schedule skew
	m.IncAuditEvent("high", "allowed")
	m.RecordAuditWriteQueueDepth(3)
	m.IncCollabPlan("parallel")
	m.RecordCollabTaskDuration("parallel", 2.0)
	m.IncOptimizerCandidate("proposal", "accepted")
	m.RecordOptimizerCycleDuration(3.0)
	m.IncOperationProposal("memory", "approved")
	m.RecordApprovalLatency("memory", 0.5)
	m.RecordScheduleSkew(1.5)
	// Reaper（注册缺陷的触发点）
	m.IncReaperCycle("ok")
	m.SetReaperCycleTimestamp(1785762800)
	m.IncReaperGuestDeleted()
	m.IncReaperDeleteError("delete_user")
	// Background components / Panics / Workflow / MCP client / Evaluation / Auth
	m.RecordComponentCycle("chat-cleanup")
	m.SetComponentCycleTimestamp("chat-cleanup", 1785762800)
	m.IncComponentError("chat-cleanup", "run")
	m.IncGoroutinePanic("memory-worker")
	m.IncWorkflowRun("tenant-1", "ok")
	m.RecordWorkflowRunDuration("tenant-1", 4.0)
	m.IncMCPClientRequest("mcp-server", "call", "ok")
	m.IncMCPClientReconnect("mcp-server")
	m.IncEvaluationJob("ok")
	m.IncAuthFailure("invalid_token")
}

func TestPrometheusMetricsAllMethodsRegistered(t *testing.T) {
	m := NewPrometheusMetrics(zap.NewNop())
	exerciseAllMetrics(m)
}

func TestNoopMetricsAllMethods(t *testing.T) {
	exerciseAllMetrics(NoopMetrics{})
}
