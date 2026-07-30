package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const maxReadinessResponseBytes = 4 << 10

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type BackendProbe struct {
	client   HTTPDoer
	endpoint string
}

func NewBackendProbe(client HTTPDoer, endpoint string) (*BackendProbe, error) {
	if client == nil {
		return nil, errors.New("backend readiness HTTP client is not configured")
	}
	if err := validateReadinessEndpoint(endpoint); err != nil {
		return nil, err
	}
	return &BackendProbe{client: client, endpoint: endpoint}, nil
}

func (p *BackendProbe) Check(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint, nil)
	if err != nil {
		return fmt.Errorf("create backend readiness request: %w", err)
	}
	response, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("call backend readiness: %w", err)
	}
	defer response.Body.Close() //nolint:errcheck -- drainAndClose reports the first close failure.
	drainErr := drainAndClose(response.Body)
	if response.StatusCode != http.StatusOK {
		return errors.Join(fmt.Errorf("backend readiness returned status %d", response.StatusCode), drainErr)
	}
	if drainErr != nil {
		return fmt.Errorf("close backend readiness response: %w", drainErr)
	}
	return nil
}

func validateReadinessEndpoint(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("parse backend readiness endpoint: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "/internal/livez" {
		return errors.New("backend readiness endpoint must be a fixed HTTPS internal livez URL")
	}
	return nil
}

func drainAndClose(body io.ReadCloser) error {
	_, readErr := io.Copy(io.Discard, io.LimitReader(body, maxReadinessResponseBytes))
	closeErr := body.Close()
	return errors.Join(readErr, closeErr)
}
