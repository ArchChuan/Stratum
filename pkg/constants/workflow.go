package constants

import "time"

const (
	WorkflowOutputDeltaMaxRunes = 1024
	WorkflowToolNameMaxRunes    = 128
	WorkflowToolSummaryMaxRunes = 256
	WorkflowOutputFlushInterval = 100 * time.Millisecond
)
