package timeutil

import "time"

// Shanghai is the Asia/Shanghai timezone location used for all timestamp serialization.
// PostgreSQL TIMESTAMPTZ stores UTC internally; this location controls Go time.Time
// formatting (JSON, logs) without affecting the stored absolute value.
var Shanghai = func() *time.Location {
	l, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.UTC
	}
	return l
}()

// Now returns current time in Asia/Shanghai timezone.
func Now() time.Time {
	return time.Now().In(Shanghai)
}
