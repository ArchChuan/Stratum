package main

import "time"

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
)
