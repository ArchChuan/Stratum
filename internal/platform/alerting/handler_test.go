package alerting

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestHandlerRejectsInvalidRequests(t *testing.T) {
	handler := newTestHandler(&recordingSender{})

	for _, tt := range []struct {
		name   string
		method string
		body   string
		status int
	}{
		{name: "method", method: http.MethodGet, status: http.StatusMethodNotAllowed},
		{name: "json", method: http.MethodPost, body: "{", status: http.StatusBadRequest},
		{name: "trailing", method: http.MethodPost, body: `{"status":"firing"} trailing`, status: http.StatusBadRequest},
		{name: "status", method: http.MethodPost, body: `{"status":"unknown"}`, status: http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(tt.method, "/alertmanager", strings.NewReader(tt.body)))
			require.Equal(t, tt.status, recorder.Code)
		})
	}
}

func TestHandlerRejectsOversizedPayload(t *testing.T) {
	handler := newTestHandler(&recordingSender{})
	recorder := httptest.NewRecorder()
	body := strings.NewReader(`{"status":"firing","padding":"` + strings.Repeat("x", maxWebhookBodyBytes) + `"}`)

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/alertmanager", body))

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
}

func TestHandlerReturnsSuccessOnlyAfterDelivery(t *testing.T) {
	sender := &recordingSender{}
	handler := newTestHandler(sender)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(
		http.MethodPost, "/alertmanager", strings.NewReader(`{"status":"firing"}`),
	))

	require.Equal(t, http.StatusNoContent, recorder.Code)
	require.Equal(t, 1, sender.calls)
}

func TestHandlerRejectsWhenDeliveryBulkheadIsFull(t *testing.T) {
	handler := newTestHandler(&recordingSender{})
	for range maxConcurrentDeliveries {
		handler.slots <- struct{}{}
	}
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(
		http.MethodPost, "/alertmanager", strings.NewReader(`{"status":"firing"}`),
	))

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

func TestHandlerPropagatesDeliveryFailure(t *testing.T) {
	sender := &recordingSender{err: errors.New("delivery unavailable")}
	handler := newTestHandler(sender)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(
		http.MethodPost, "/alertmanager", strings.NewReader(`{"status":"firing"}`),
	))

	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "delivery unavailable")
}

func newTestHandler(sender Sender) *Handler {
	return NewHandler(sender, "https://example.invalid/hook", NewMetrics(prometheus.NewRegistry()))
}

type recordingSender struct {
	calls int
	err   error
}

func (s *recordingSender) Send(context.Context, string, FeishuMessage) error {
	s.calls++
	return s.err
}
