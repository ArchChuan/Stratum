package alerting

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestDeliverStopsOnPermanentClientError(t *testing.T) {
	doer := &sequenceDoer{results: []doResult{{status: http.StatusBadRequest}, {status: http.StatusOK}}}
	delivery := NewDelivery(doer, noSleep)

	err := delivery.Send(context.Background(), "https://example.invalid/hook", FeishuMessage{})

	require.Error(t, err)
	require.Equal(t, 1, doer.calls)
}

func TestDeliverRetriesTransientStatusAndSucceeds(t *testing.T) {
	doer := &sequenceDoer{results: []doResult{{status: http.StatusServiceUnavailable}, {status: http.StatusOK}}}
	delivery := NewDelivery(doer, noSleep)

	err := delivery.Send(context.Background(), "https://example.invalid/hook", FeishuMessage{})

	require.NoError(t, err)
	require.Equal(t, 2, doer.calls)
}

func TestDeliverCountsRetryAttempts(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)
	doer := &sequenceDoer{results: []doResult{{status: http.StatusServiceUnavailable}, {status: http.StatusOK}}}
	delivery := NewDeliveryWithMetrics(doer, noSleep, metrics)

	require.NoError(t, delivery.Send(context.Background(), "https://example.invalid/hook", FeishuMessage{}))
	families, err := registry.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() == "feishu_alert_delivery_retries_total" {
			require.Equal(t, float64(1), family.Metric[0].Counter.GetValue())
			return
		}
	}
	t.Fatal("retry metric was not registered")
}

func TestDeliverRetriesTransportFailureAtMostThreeTimes(t *testing.T) {
	doer := &sequenceDoer{results: []doResult{
		{err: errors.New("network unavailable")},
		{err: errors.New("network unavailable")},
		{err: errors.New("network unavailable")},
		{status: http.StatusOK},
	}}
	delivery := NewDelivery(doer, noSleep)

	err := delivery.Send(context.Background(), "https://example.invalid/hook", FeishuMessage{})

	require.Error(t, err)
	require.Equal(t, maxDeliveryAttempts, doer.calls)
	require.NotContains(t, err.Error(), "example.invalid")
}

func TestDeliverStopsWhenContextCancelsDuringBackoff(t *testing.T) {
	doer := &sequenceDoer{results: []doResult{{status: http.StatusTooManyRequests}}}
	delivery := NewDelivery(doer, func(ctx context.Context, _ time.Duration) error {
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := delivery.Send(ctx, "https://example.invalid/hook", FeishuMessage{})

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, doer.calls)
}

func TestDeliverClosesEveryResponseBody(t *testing.T) {
	first := &trackingReadCloser{Reader: strings.NewReader(`{}`)}
	second := &trackingReadCloser{Reader: strings.NewReader(`{}`)}
	doer := &sequenceDoer{results: []doResult{
		{status: http.StatusInternalServerError, body: first},
		{status: http.StatusOK, body: second},
	}}
	delivery := NewDelivery(doer, noSleep)

	require.NoError(t, delivery.Send(context.Background(), "https://example.invalid/hook", FeishuMessage{}))
	require.True(t, first.closed)
	require.True(t, second.closed)
}

func TestDeliverRejectsFeishuApplicationError(t *testing.T) {
	doer := &sequenceDoer{results: []doResult{{
		status: http.StatusOK,
		body:   io.NopCloser(strings.NewReader(`{"code":19001,"msg":"invalid webhook"}`)),
	}}}
	delivery := NewDelivery(doer, noSleep)

	err := delivery.Send(context.Background(), "https://example.invalid/hook", FeishuMessage{})

	require.Error(t, err)
	require.Equal(t, 1, doer.calls)
	require.NotContains(t, err.Error(), "invalid webhook")
}

type doResult struct {
	status int
	body   io.ReadCloser
	err    error
}

type sequenceDoer struct {
	results []doResult
	calls   int
}

func (d *sequenceDoer) Do(*http.Request) (*http.Response, error) {
	result := d.results[min(d.calls, len(d.results)-1)]
	d.calls++
	if result.err != nil {
		return nil, result.err
	}
	body := result.body
	if body == nil {
		body = io.NopCloser(strings.NewReader("{}"))
	}
	return &http.Response{StatusCode: result.status, Body: body}, nil
}

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}

func noSleep(context.Context, time.Duration) error { return nil }
