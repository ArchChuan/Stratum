package alerting

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

const (
	maxWebhookBodyBytes     = 1 << 20
	maxConcurrentDeliveries = 8
)

type Sender interface {
	Send(context.Context, string, FeishuMessage) error
}

type Handler struct {
	sender     Sender
	webhookURL string
	metrics    *Metrics
	slots      chan struct{}
}

func NewHandler(sender Sender, webhookURL string, metrics *Metrics) *Handler {
	return &Handler{
		sender: sender, webhookURL: webhookURL, metrics: metrics,
		slots: make(chan struct{}, maxConcurrentDeliveries),
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		http.Error(w, "alert delivery capacity exhausted", http.StatusServiceUnavailable)
		return
	}
	h.metrics.requestsInFlight.Inc()
	defer h.metrics.requestsInFlight.Dec()
	startedAt := time.Now()
	reader := http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes)
	defer reader.Close()
	var group AlertGroup
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&group); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "alert payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid alert payload", http.StatusBadRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid alert payload", http.StatusBadRequest)
		return
	}
	message, err := RenderCard(group)
	if err != nil {
		http.Error(w, "invalid alert payload", http.StatusBadRequest)
		return
	}
	if err := h.sender.Send(r.Context(), h.webhookURL, message); err != nil {
		h.metrics.deliveryTotal.WithLabelValues("failed").Inc()
		h.metrics.deliveryDuration.Observe(time.Since(startedAt).Seconds())
		http.Error(w, "alert delivery failed", http.StatusBadGateway)
		return
	}
	h.metrics.deliveryTotal.WithLabelValues("success").Inc()
	h.metrics.deliveryDuration.Observe(time.Since(startedAt).Seconds())
	w.WriteHeader(http.StatusNoContent)
}
