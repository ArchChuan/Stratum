package main

import (
	"time"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// Tool behavior numbers, centralized per the constants convention. No inline
// magic numbers in the orchestration below.
const (
	DefaultWarnDelta         = 0.1
	DefaultHTTPTimeout       = 30 * time.Second
	DefaultIngestPollTimeout = 90 * time.Second
	DefaultIngestPollEvery   = 3 * time.Second
	DefaultJWTExpiry         = 30 * time.Minute
	DefaultWorkspacePrefix   = "rag-check-"
	MaxCaseQueryChars        = 4096 // mirrors gen.QueryRequest binding max

	gitCommandTimeout = 5 * time.Second

	exitPassed = 0
	exitFailed = 1
	exitInfra  = 2
)

// metricTopK mirrors knowledgeapp.RetrievalK (constants.DefaultRAGTopK). The
// golden snapshot asserts equality so HTTP topK stays within the binding
// max=20 while the workspace config keeps the domain default.
var metricTopK = constants.DefaultRAGTopK

// HTTPTopKMax mirrors the generated QueryRequest binding max=20. Keeping the
// contract explicit here means a proto contract change surfaces in tests, not
// as a silent 400 at runtime.
const HTTPTopKMax = 20
