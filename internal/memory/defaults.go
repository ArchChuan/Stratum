package memory

import "time"

const (
	DefaultPersistInterval = 5 * time.Minute
	DefaultMaxMemoryAge    = 30 * 24 * time.Hour
	DefaultSearchLimit     = 20
)
