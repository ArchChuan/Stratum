package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/platform/alerting"
	"github.com/byteBuilderX/stratum/pkg/httpclient"
)

const (
	monitorBudget    = 30 * time.Second
	requestTimeout   = 8 * time.Second
	maxProbeAttempts = 3
	maxResponseBytes = 64 << 10
	issueLabel       = "remote-health-monitor"
	issueTitle       = "[monitoring] Remote Stratum health check failing"
	githubAPIBase    = "https://api.github.com"
	expectedService  = "Stratum"
	expectedStatus   = "ok"
	githubAPIVersion = "2022-11-28"
	githubAccept     = "application/vnd.github+json"
)

type diagnosticCategory string

const (
	diagnosticNone       diagnosticCategory = ""
	diagnosticTransport  diagnosticCategory = "transport"
	diagnosticHTTPStatus diagnosticCategory = "http_status"
	diagnosticContract   diagnosticCategory = "contract"
)

type probe interface {
	Check(context.Context) diagnosticCategory
}

type issue struct {
	Number int `json:"number"`
}

type issueStore interface {
	FindOpen(context.Context) (*issue, error)
	Create(context.Context, string) error
	Update(context.Context, int, string) error
	Close(context.Context, int, string) error
}

type messageSender interface {
	Send(context.Context, alerting.FeishuMessage) error
}

type httpProbe struct {
	client *http.Client
	url    string
}

func (p *httpProbe) Check(ctx context.Context) diagnosticCategory {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return diagnosticTransport
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return diagnosticTransport
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return diagnosticHTTPStatus
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return diagnosticContract
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var health struct {
		Service string `json:"service"`
		Status  string `json:"status"`
	}
	if err := decoder.Decode(&health); err != nil || health.Service != expectedService || health.Status != expectedStatus {
		return diagnosticContract
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return diagnosticContract
	}
	return diagnosticNone
}

type githubIssues struct {
	client  *http.Client
	baseURL string
	repo    string
	token   string
}

func (g *githubIssues) FindOpen(ctx context.Context) (*issue, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/issues?state=open&labels=%s&per_page=2", g.baseURL, g.repo, issueLabel)
	var issues []issue
	if err := g.doJSON(ctx, http.MethodGet, endpoint, nil, &issues); err != nil {
		return nil, fmt.Errorf("query monitor issue: %w", err)
	}
	if len(issues) == 0 {
		return nil, nil
	}
	if len(issues) > 1 {
		return nil, errors.New("query monitor issue: duplicate open issues")
	}
	return &issues[0], nil
}

func (g *githubIssues) Create(ctx context.Context, body string) error {
	payload := struct {
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Labels []string `json:"labels"`
	}{Title: issueTitle, Body: body, Labels: []string{issueLabel}}
	return g.doJSON(ctx, http.MethodPost, fmt.Sprintf("%s/repos/%s/issues", g.baseURL, g.repo), payload, nil)
}

func (g *githubIssues) Update(ctx context.Context, number int, body string) error {
	return g.patch(ctx, number, struct {
		Body string `json:"body"`
	}{Body: body})
}

func (g *githubIssues) Close(ctx context.Context, number int, body string) error {
	return g.patch(ctx, number, struct {
		Body  string `json:"body"`
		State string `json:"state"`
	}{Body: body, State: "closed"})
}

func (g *githubIssues) patch(ctx context.Context, number int, payload any) error {
	endpoint := fmt.Sprintf("%s/repos/%s/issues/%d", g.baseURL, g.repo, number)
	return g.doJSON(ctx, http.MethodPatch, endpoint, payload, nil)
}

func (g *githubIssues) doJSON(ctx context.Context, method, endpoint string, payload, result any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return errors.New("encode GitHub request")
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return errors.New("build GitHub request")
	}
	req.Header.Set("Accept", githubAccept)
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return errors.New("GitHub transport failure")
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("GitHub non-success status=%d", resp.StatusCode)
	}
	if result != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes+1)).Decode(result); err != nil {
			return errors.New("decode GitHub response")
		}
	}
	return nil
}

type feishuSender struct {
	delivery   *alerting.Delivery
	webhookURL string
}

func (s *feishuSender) Send(ctx context.Context, message alerting.FeishuMessage) error {
	return s.delivery.Send(ctx, s.webhookURL, message)
}

func monitor(ctx context.Context, health probe, issues issueStore, sender messageSender, now func() time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, monitorBudget)
	defer cancel()

	category := diagnosticNone
	for attempt := 0; attempt < maxProbeAttempts; attempt++ {
		category = health.Check(ctx)
		if category == diagnosticNone {
			break
		}
	}
	openIssue, err := issues.FindOpen(ctx)
	if err != nil {
		return err
	}
	timestamp := now().UTC().Format(time.RFC3339)
	if category == diagnosticNone {
		if openIssue == nil {
			return nil
		}
		message, err := renderMessage("resolved", timestamp, diagnosticNone)
		if err != nil {
			return err
		}
		if err := sender.Send(ctx, message); err != nil {
			return fmt.Errorf("send resolved notification: %w", err)
		}
		if err := issues.Close(ctx, openIssue.Number, issueBody(timestamp, "resolved", diagnosticNone)); err != nil {
			return fmt.Errorf("close monitor issue: %w", err)
		}
		return nil
	}

	body := issueBody(timestamp, "firing", category)
	if openIssue != nil {
		if err := issues.Update(ctx, openIssue.Number, body); err != nil {
			return fmt.Errorf("update monitor issue: %w", err)
		}
		return nil
	}
	if err := issues.Create(ctx, body); err != nil {
		return fmt.Errorf("create monitor issue: %w", err)
	}
	message, err := renderMessage("firing", timestamp, category)
	if err != nil {
		return err
	}
	if err := sender.Send(ctx, message); err != nil {
		return fmt.Errorf("send firing notification: %w", err)
	}
	return nil
}

func issueBody(timestamp, status string, category diagnosticCategory) string {
	return fmt.Sprintf("timestamp: %s\nstatus: %s\ndiagnostic: %s\n", timestamp, status, displayCategory(category))
}

func renderMessage(status, timestamp string, category diagnosticCategory) (alerting.FeishuMessage, error) {
	return alerting.RenderCard(alerting.AlertGroup{
		Status: status,
		CommonLabels: map[string]string{
			"alertname":   "RemoteStratumHealth",
			"severity":    "critical",
			"service":     expectedService,
			"environment": "remote",
		},
		CommonAnnotations: map[string]string{
			"summary":     "远程 Stratum 健康检查状态变更",
			"description": fmt.Sprintf("timestamp=%s diagnostic=%s", timestamp, displayCategory(category)),
		},
	})
}

func displayCategory(category diagnosticCategory) string {
	if category == diagnosticNone {
		return "none"
	}
	return string(category)
}

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	healthURL := os.Getenv("REMOTE_HEALTH_URL")
	webhookURL := os.Getenv("FEISHU_WEBHOOK_URL")
	token := os.Getenv("GITHUB_TOKEN")
	repo := os.Getenv("GITHUB_REPOSITORY")
	if err := validateConfig(healthURL, webhookURL, token, repo); err != nil {
		return err
	}
	client := httpclient.New(httpclient.WithTimeout(requestTimeout), httpclient.WithDisableRedirects())
	return monitor(context.Background(), &httpProbe{client: client, url: healthURL}, &githubIssues{
		client: client, baseURL: githubAPIBase, repo: repo, token: token,
	}, &feishuSender{delivery: alerting.NewDelivery(client, nil), webhookURL: webhookURL}, time.Now)
}

func validateConfig(healthURL, webhookURL, token, repo string) error {
	if token == "" || repo == "" || !strings.Contains(repo, "/") {
		return errors.New("GitHub monitor configuration is incomplete")
	}
	if err := validateHTTPSURL(healthURL, false); err != nil {
		return errors.New("REMOTE_HEALTH_URL must be a valid HTTPS URL")
	}
	if err := validateHTTPSURL(webhookURL, true); err != nil {
		return errors.New("FEISHU_WEBHOOK_URL must be a valid HTTPS URL")
	}
	return nil
}

func validateHTTPSURL(raw string, feishuOnly bool) error {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" || parsed.Host == "" {
		return errors.New("invalid URL")
	}
	if feishuOnly && parsed.Hostname() != "open.feishu.cn" && parsed.Hostname() != "open.larksuite.com" {
		return errors.New("invalid Feishu host")
	}
	return nil
}
