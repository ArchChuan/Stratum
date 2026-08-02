package workers

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	WorkerMessagesProcessed  *prometheus.CounterVec
	WorkerProcessingDuration *prometheus.HistogramVec
	workerPanics             *prometheus.CounterVec
)

func incWorkerMessages(worker, tenant, status string) {
	if WorkerMessagesProcessed != nil {
		WorkerMessagesProcessed.WithLabelValues(worker, tenant, status).Inc()
	}
}

func observeWorkerDuration(worker, tenant string, secs float64) {
	if WorkerProcessingDuration != nil {
		WorkerProcessingDuration.WithLabelValues(worker, tenant).Observe(secs)
	}
}

func incWorkerPanics(worker string) {
	if workerPanics != nil {
		workerPanics.WithLabelValues(worker).Inc()
	}
}

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
	workerPanics = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "memory_worker_panics_total",
			Help: "Total goroutine panics recovered in memory workers",
		},
		[]string{"worker"},
	)
	reg.MustRegister(WorkerMessagesProcessed, WorkerProcessingDuration, workerPanics)
}
