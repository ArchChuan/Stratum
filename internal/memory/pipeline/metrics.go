package pipeline

import "github.com/prometheus/client_golang/prometheus"

var (
	outboxPending = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "memory_outbox_pending",
		Help: "Number of pending outbox messages",
	})
	outboxPublished = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "memory_outbox_published_total",
		Help: "Total outbox messages published",
	}, []string{"tenant_id", "status"})
	embedDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "memory_embed_duration_seconds",
		Help:    "Embedding processing duration",
		Buckets: prometheus.DefBuckets,
	})
	embedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "memory_embed_total",
		Help: "Total embed operations",
	}, []string{"tenant_id", "status"})
	enrichDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "memory_enrich_duration_seconds",
		Help:    "Enrichment processing duration",
		Buckets: prometheus.DefBuckets,
	})
	enrichTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "memory_enrich_total",
		Help: "Total enrich operations",
	}, []string{"tenant_id", "status"})
	summaryTriggered = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "memory_summary_triggered_total",
		Help: "Total summary generations triggered",
	})
	dlqTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "memory_dlq_total",
		Help: "Total messages sent to DLQ",
	}, []string{"tenant_id", "stage"})
	entitiesExtracted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "memory_entities_extracted_total",
		Help: "Total entities extracted",
	})
)

// RegisterMetrics registers all pipeline metrics with the given registerer.
func RegisterMetrics(reg prometheus.Registerer) {
	reg.MustRegister(
		outboxPending, outboxPublished,
		embedDuration, embedTotal,
		enrichDuration, enrichTotal,
		summaryTriggered, dlqTotal, entitiesExtracted,
	)
}
