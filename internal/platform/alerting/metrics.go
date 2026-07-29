package alerting

import "github.com/prometheus/client_golang/prometheus"

type Metrics struct {
	deliveryTotal    *prometheus.CounterVec
	deliveryDuration prometheus.Histogram
	retriesTotal     prometheus.Counter
	requestsInFlight prometheus.Gauge
}

func NewMetrics(registerer prometheus.Registerer) *Metrics {
	metrics := &Metrics{
		deliveryTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "feishu_alert_delivery_total",
			Help: "Total Feishu alert delivery outcomes.",
		}, []string{"status"}),
		deliveryDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "feishu_alert_delivery_duration_seconds",
			Help: "Feishu alert delivery duration in seconds.",
		}),
		retriesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "feishu_alert_delivery_retries_total",
			Help: "Total Feishu alert delivery retry attempts.",
		}),
		requestsInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "feishu_alert_requests_in_flight",
			Help: "Current Alertmanager webhook requests being handled.",
		}),
	}
	registerer.MustRegister(
		metrics.deliveryTotal,
		metrics.deliveryDuration,
		metrics.retriesTotal,
		metrics.requestsInFlight,
	)
	return metrics
}
