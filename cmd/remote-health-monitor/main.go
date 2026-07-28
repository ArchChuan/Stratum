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
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

type issueStore interface {
	FindOpen(context.Context) (*issue, error)
	Create(context.Context, string) (*issue, error)
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
	if issues[0].Title != issueTitle {
		return nil, errors.New("query monitor issue: title mismatch")
	}
	return &issues[0], nil
}

func (g *githubIssues) Create(ctx context.Context, body string) (*issue, error) {
	payload := struct {
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Labels []string `json:"labels"`
	}{Title: issueTitle, Body: body, Labels: []string{issueLabel}}
	var created issue
	if err := g.doJSON(ctx, http.MethodPost, fmt.Sprintf("%s/repos/%s/issues", g.baseURL, g.repo), payload, &created); err != nil {
		return nil, err
	}
	if created.Number <= 0 || created.Title != issueTitle || created.Body != body {
		return nil, errors.New("create monitor issue: invalid response")
	}
	return &created, nil
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
	// GitHub issue state and Feishu delivery cannot be committed atomically. The outbox chooses
	// no-loss, at-least-once delivery and keeps each pending event payload stable across retries.

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
	var state *issueState
	if openIssue != nil {
		if openIssue.Title != issueTitle {
			return errors.New("monitor issue title mismatch")
		}
		state, err = parseIssueBody(openIssue.Body)
		if err != nil {
			return fmt.Errorf("parse monitor issue state: %w", err)
		}
	}
	if category == diagnosticNone {
		if openIssue == nil {
			return nil
		}
		if state.Status == "resolved" && state.Notification == "sent" {
			if err := issues.Close(ctx, openIssue.Number, openIssue.Body); err != nil {
				return fmt.Errorf("close monitor issue: %w", err)
			}
			return nil
		}
		eventTimestamp := timestamp
		if state.Status == "resolved" && state.Notification == "pending" {
			eventTimestamp = state.Timestamp.UTC().Format(time.RFC3339)
		}
		pendingBody := issueBody(eventTimestamp, "resolved", diagnosticNone, "pending")
		if state.Status != "resolved" || state.Notification != "pending" {
			if err := issues.Update(ctx, openIssue.Number, pendingBody); err != nil {
				return fmt.Errorf("persist resolved pending state: %w", err)
			}
		}
		message, err := renderMessage("resolved", eventTimestamp, diagnosticNone)
		if err != nil {
			return err
		}
		if err := sender.Send(ctx, message); err != nil {
			return fmt.Errorf("send resolved notification: %w", err)
		}
		sentBody := issueBody(eventTimestamp, "resolved", diagnosticNone, "sent")
		if err := issues.Update(ctx, openIssue.Number, sentBody); err != nil {
			return fmt.Errorf("persist resolved sent state: %w", err)
		}
		if err := issues.Close(ctx, openIssue.Number, sentBody); err != nil {
			return fmt.Errorf("close monitor issue: %w", err)
		}
		return nil
	}

	eventTimestamp := timestamp
	eventCategory := category
	if openIssue != nil {
		if state.Status == "firing" && state.Notification == "sent" {
			if err := issues.Update(ctx, openIssue.Number, issueBody(timestamp, "firing", category, "sent")); err != nil {
				return fmt.Errorf("update monitor issue: %w", err)
			}
			return nil
		}
		if state.Status == "firing" && state.Notification == "pending" {
			eventTimestamp = state.Timestamp.UTC().Format(time.RFC3339)
			eventCategory = state.Diagnostic
		}
		pendingBody := issueBody(eventTimestamp, "firing", eventCategory, "pending")
		if state.Status != "firing" || state.Notification != "pending" {
			if err := issues.Update(ctx, openIssue.Number, pendingBody); err != nil {
				return fmt.Errorf("persist firing pending state: %w", err)
			}
		}
	} else {
		pendingBody := issueBody(eventTimestamp, "firing", eventCategory, "pending")
		openIssue, err = issues.Create(ctx, pendingBody)
		if err != nil {
			return fmt.Errorf("create monitor issue: %w", err)
		}
	}
	message, err := renderMessage("firing", eventTimestamp, eventCategory)
	if err != nil {
		return err
	}
	if err := sender.Send(ctx, message); err != nil {
		return fmt.Errorf("send firing notification: %w", err)
	}
	if err := issues.Update(ctx, openIssue.Number, issueBody(eventTimestamp, "firing", eventCategory, "sent")); err != nil {
		return fmt.Errorf("persist firing sent state: %w", err)
	}
	return nil
}

type issueState struct {
	Timestamp    time.Time
	Status       string
	Diagnostic   diagnosticCategory
	Notification string
}

func issueBody(timestamp, status string, category diagnosticCategory, notification string) string {
	return fmt.Sprintf(
		"timestamp: %s\nstatus: %s\ndiagnostic: %s\nnotification: %s\n",
		timestamp, status, displayCategory(category), notification,
	)
}

func parseIssueBody(body string) (*issueState, error) {
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if len(lines) != 4 {
		return nil, errors.New("invalid field count")
	}
	values := make([]string, 4)
	keys := []string{"timestamp: ", "status: ", "diagnostic: ", "notification: "}
	for index, key := range keys {
		if !strings.HasPrefix(lines[index], key) {
			return nil, errors.New("invalid field")
		}
		values[index] = strings.TrimPrefix(lines[index], key)
	}
	timestamp, err := time.Parse(time.RFC3339, values[0])
	if err != nil {
		return nil, errors.New("invalid timestamp")
	}
	if values[1] != "firing" && values[1] != "resolved" {
		return nil, errors.New("invalid status")
	}
	category := diagnosticCategory(values[2])
	if values[2] == "none" {
		category = diagnosticNone
	}
	if category != diagnosticNone && category != diagnosticTransport && category != diagnosticHTTPStatus && category != diagnosticContract {
		return nil, errors.New("invalid diagnostic")
	}
	if values[1] == "resolved" && category != diagnosticNone {
		return nil, errors.New("invalid resolved diagnostic")
	}
	if values[1] == "firing" && category == diagnosticNone {
		return nil, errors.New("invalid firing diagnostic")
	}
	if values[3] != "pending" && values[3] != "sent" {
		return nil, errors.New("invalid notification state")
	}
	return &issueState{Timestamp: timestamp, Status: values[1], Diagnostic: category, Notification: values[3]}, nil
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
