package workers

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	WorkerMessagesProcessed  *prometheus.CounterVec
	WorkerProcessingDuration *prometheus.HistogramVec
)

// RegisterMetrics registers worker metrics with the given registerer.
func RegisterMetrics(reg prometheus.Registerer) {
	WorkerMessagesProcessed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "memory_worker_messages_total",
			Help: "Total messages processed by memory workers",
		},
		[]string{"worker", "tenant_id", "status"},
	)
	WorkerProcessingDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "memory_worker_processing_seconds",
			Help:    "Time spent processing messages in memory workers",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"worker", "tenant_id"},
	)
	reg.MustRegister(WorkerMessagesProcessed, WorkerProcessingDuration)
}
