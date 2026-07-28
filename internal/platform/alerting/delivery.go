package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/byteBuilderX/stratum/pkg/httpclient"
)

const (
	maxDeliveryAttempts = 3
	deliveryBudget      = 10 * time.Second
	deliveryBackoffBase = 100 * time.Millisecond
	deliveryBackoffMax  = 2 * time.Second
	maxResponseBytes    = 64 << 10
)

type sleepFunc func(context.Context, time.Duration) error

type Delivery struct {
	doer    httpclient.Doer
	sleep   sleepFunc
	metrics *Metrics
}

func NewDelivery(doer httpclient.Doer, sleep sleepFunc) *Delivery {
	return NewDeliveryWithMetrics(doer, sleep, nil)
}

func NewDeliveryWithMetrics(doer httpclient.Doer, sleep sleepFunc, metrics *Metrics) *Delivery {
	if sleep == nil {
		sleep = sleepContext
	}
	return &Delivery{doer: doer, sleep: sleep, metrics: metrics}
}

func (d *Delivery) Send(ctx context.Context, webhookURL string, message FeishuMessage) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal Feishu message: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, deliveryBudget)
	defer cancel()

	for attempt := 1; attempt <= maxDeliveryAttempts; attempt++ {
		if attempt > 1 {
			if d.metrics != nil {
				d.metrics.retriesTotal.Inc()
			}
			delay := min(deliveryBackoffBase<<(attempt-2), deliveryBackoffMax)
			if err := d.sleep(ctx, delay); err != nil {
				return err
			}
		}
		status, retry, err := d.attempt(ctx, webhookURL, payload)
		if err == nil {
			return nil
		}
		if !retry || attempt == maxDeliveryAttempts {
			return fmt.Errorf("deliver Feishu alert: status=%d attempts=%d: %w", status, attempt, err)
		}
	}
	return errors.New("deliver Feishu alert: retry loop exhausted")
}

func (d *Delivery) attempt(ctx context.Context, webhookURL string, payload []byte) (int, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(payload))
	if err != nil {
		return 0, false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.doer.Do(req)
	if err != nil {
		return 0, true, errors.New("transport failure")
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if readErr != nil {
		return resp.StatusCode, true, errors.New("read response failure")
	}
	if len(body) > maxResponseBytes {
		return resp.StatusCode, false, errors.New("response too large")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resp.StatusCode, isRetryableStatus(resp.StatusCode), errors.New("non-success response")
	}
	var result struct {
		Code int `json:"code"`
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &result); err != nil {
			return resp.StatusCode, false, errors.New("invalid Feishu response")
		}
		if result.Code != 0 {
			return resp.StatusCode, false, errors.New("Feishu rejected message")
		}
	}
	return resp.StatusCode, false, nil
}

func isRetryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
