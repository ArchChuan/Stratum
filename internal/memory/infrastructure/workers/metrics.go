package workers

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	WorkerMessagesProcessed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "memory_worker_messages_total",
			Help: "Total messages processed by memory workers",
		},
		[]string{"worker", "tenant_id", "status"},
	)

	WorkerProcessingDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "memory_worker_processing_seconds",
			Help:    "Time spent processing messages in memory workers",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"worker", "tenant_id"},
	)
)
