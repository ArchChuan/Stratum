package constants

import "time"

const (
	DefaultOpikTimeout        = 5 * time.Second
	MaxOpikResponseBytes      = 8 * 1024 * 1024
	DefaultTracePayloadBucket = "stratum-trace-evidence"
)
