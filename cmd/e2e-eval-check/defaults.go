package main

import (
	"time"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

const (
	// DefaultWarnDelta is the regression threshold: a run metric below
	// baseline - delta is a regression.
	DefaultWarnDelta    = 0.1
	DefaultHTTPTimeout  = 30 * time.Second
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
