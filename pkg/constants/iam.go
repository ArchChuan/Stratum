package constants

import "time"

const (
	// GuestProvisionMaxAttempts bounds retries for transient PostgreSQL failures.
	GuestProvisionMaxAttempts = 3
	// GuestProvisionRetryBackoff is the delay between guest provisioning attempts.
	GuestProvisionRetryBackoff = 100 * time.Millisecond
)
