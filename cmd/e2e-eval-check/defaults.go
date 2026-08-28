package main

import (
	"time"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

const (
	// DefaultWarnDelta is the regression threshold: a run metric below
	// baseline - delta is a regression.
	DefaultWarnDelta = 0.1
	// DefaultHTTPTimeout bounds the eval CLI's per-request HTTP timeout. Agent
	// execute with a real LLM legitimately exceeds 30s under provider latency
	// (observed 34s on glm-4-flash); 30s caused flaky infra aborts in
	// acceptance E2E. Bumped to 120s so a slow-but-successful run is not a
	// false infra failure. Judge calls stay within the same bound.
	DefaultHTTPTimeout  = 120 * time.Second
	DefaultJWTExpiry    = 30 * time.Minute
	exitPassed          = 0
	exitFailed          = 1
	exitInfraFailed     = 2
	reportSchemaVersion = 1
	// DefaultIngestPollTimeout bounds the workspace document ingest wait.
	DefaultIngestPollTimeout = 90 * time.Second
	// DefaultIngestPollEvery is the ingest-status poll interval.
	DefaultIngestPollEvery = 2 * time.Second
	// DefaultWorkspacePrefix names transient eval workspaces.
	DefaultWorkspacePrefix = "eval-check-"
	// DefaultCarrierAgentMaxIterations bounds the transient carrier agent used
	// by skill eval; must stay within the agent create contract's [1,90] range.
	DefaultCarrierAgentMaxIterations = 10
	// DefaultCarrierAgentMemoryScope is the required memory_scope of the carrier
	// agent; "user" matches the frontend create-agent default.
	DefaultCarrierAgentMemoryScope = "user"
)

// metricTopK mirrors knowledgeapp.RetrievalK (constants.DefaultRAGTopK). The
// golden snapshot asserts equality so HTTP topK stays within the binding
// max=20 while the workspace config keeps the domain default.
var metricTopK = constants.DefaultRAGTopK

// HTTPTopKMax mirrors the generated QueryRequest binding max=20. Keeping the
// contract explicit here means a proto contract change surfaces in tests, not
// as a silent 400 at runtime.
const HTTPTopKMax = 20

// MaxCaseQueryChars mirrors the generated QueryRequest binding max=4096.
const MaxCaseQueryChars = 4096
