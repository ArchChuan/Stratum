// Package observability provides monitoring and tracing.

package observability

// Meter is a thin interface for recording raw numeric measurements.
// Use MetricsProvider for domain-specific metrics.
type Meter interface {
	RecordInt64(name string, value int64)
	RecordFloat64(name string, value float64)
}

type noopMeter struct{}

func (noopMeter) RecordInt64(_ string, _ int64)     {}
func (noopMeter) RecordFloat64(_ string, _ float64) {}

// GetMeter returns a no-op Meter. Replace with a real implementation as needed.
func GetMeter() Meter { return noopMeter{} }
