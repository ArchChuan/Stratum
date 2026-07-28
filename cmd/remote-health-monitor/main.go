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
	probeBackoffBase = 100 * time.Millisecond
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
	Check(context.Context) probeResult
}

type probeResult struct {
	category diagnosticCategory
	retry    bool
}

type issue struct {
	Number int     `json:"number"`
	Title  string  `json:"title"`
	Body   string  `json:"body"`
	Labels []label `json:"labels"`
}

type label struct {
	Name string `json:"name"`
}

type issueStore interface {
	EnsureLabel(context.Context) error
	FindOpen(context.Context) (*issue, error)
	Create(context.Context, string) (*issue, error)
	Update(context.Context, int, string) error
	Close(context.Context, int, string) error
}

func (g *githubIssues) EnsureLabel(ctx context.Context) error {
	endpoint := fmt.Sprintf("%s/repos/%s/labels/%s", g.baseURL, g.repo, issueLabel)
	var existing label
	status, err := g.doJSONStatus(ctx, http.MethodGet, endpoint, nil, &existing)
	if err == nil && status == http.StatusOK {
		if existing.Name != issueLabel {
			return errors.New("ensure monitor label: invalid response")
		}
		return nil
	}
	if status != http.StatusNotFound {
		return fmt.Errorf("ensure monitor label: %w", err)
	}
	payload := struct {
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description"`
	}{Name: issueLabel, Color: "d73a4a", Description: "External remote health monitor durable state"}
	var created label
	if _, err := g.doJSONStatus(ctx, http.MethodPost, fmt.Sprintf("%s/repos/%s/labels", g.baseURL, g.repo), payload, &created); err != nil {
		return fmt.Errorf("create monitor label: %w", err)
	}
	if created.Name != issueLabel {
		return errors.New("create monitor label: invalid response")
	}
	return nil
}

type messageSender interface {
	Send(context.Context, alerting.FeishuMessage) error
}

type httpProbe struct {
	client *http.Client
	url    string
}

func (p *httpProbe) Check(ctx context.Context) probeResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return probeResult{category: diagnosticTransport, retry: true}
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return probeResult{category: diagnosticTransport, retry: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return probeResult{
			category: diagnosticHTTPStatus,
			retry:    resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500,
		}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return probeResult{category: diagnosticContract}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var health struct {
		Service string `json:"service"`
		Status  string `json:"status"`
	}
	if err := decoder.Decode(&health); err != nil || health.Service != expectedService || health.Status != expectedStatus {
		return probeResult{category: diagnosticContract}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return probeResult{category: diagnosticContract}
	}
	return probeResult{}
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
	if !hasMonitorLabel(issues[0].Labels) {
		return nil, errors.New("query monitor issue: label missing")
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
	if created.Number <= 0 || created.Title != issueTitle || created.Body != body || !hasMonitorLabel(created.Labels) {
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
	_, err := g.doJSONStatus(ctx, method, endpoint, payload, result)
	return err
}

func (g *githubIssues) doJSONStatus(ctx context.Context, method, endpoint string, payload, result any) (int, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, errors.New("encode GitHub request")
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return 0, errors.New("build GitHub request")
	}
	req.Header.Set("Accept", githubAccept)
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return 0, errors.New("GitHub transport failure")
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resp.StatusCode, fmt.Errorf("GitHub non-success status=%d", resp.StatusCode)
	}
	if result != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes+1)).Decode(result); err != nil {
			return resp.StatusCode, errors.New("decode GitHub response")
		}
	}
	return resp.StatusCode, nil
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

	if err := issues.EnsureLabel(ctx); err != nil {
		return err
	}
	category := diagnosticNone
	for attempt := 0; attempt < maxProbeAttempts; attempt++ {
		outcome := health.Check(ctx)
		category = outcome.category
		if category == diagnosticNone || !outcome.retry {
			break
		}
		if attempt < maxProbeAttempts-1 {
			if err := sleepContext(ctx, probeBackoffBase<<attempt); err != nil {
				return err
			}
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
		if !hasMonitorLabel(openIssue.Labels) {
			return errors.New("monitor issue label missing")
		}
		state, err = parseIssueBody(openIssue.Body)
		if err != nil {
			return fmt.Errorf("parse monitor issue state: %w", err)
		}
	}
	if openIssue != nil && state.Notification == "pending" {
		sentBody, err := deliverPending(ctx, issues, sender, openIssue, state)
		if err != nil {
			return err
		}
		state.Notification, openIssue.Body = "sent", sentBody
		if state.Status == "resolved" {
			if err := issues.Close(ctx, openIssue.Number, sentBody); err != nil {
				return fmt.Errorf("close monitor issue: %w", err)
			}
			openIssue, state = nil, nil
		}
	}
	if category == diagnosticNone {
		if openIssue == nil {
			return nil
		}
		if state.Status == "resolved" {
			return closeIssue(ctx, issues, openIssue)
		}
		return transitionAndDeliver(ctx, issues, sender, openIssue, timestamp, "resolved", diagnosticNone, true)
	}
	if openIssue == nil {
		return createAndDeliver(ctx, issues, sender, timestamp, category)
	}
	if state.Status == "resolved" {
		if err := closeIssue(ctx, issues, openIssue); err != nil {
			return err
		}
		return createAndDeliver(ctx, issues, sender, timestamp, category)
	}
	return issues.Update(ctx, openIssue.Number, issueBody(timestamp, "firing", category, "sent"))
}

func deliverPending(ctx context.Context, issues issueStore, sender messageSender, item *issue, state *issueState) (string, error) {
	timestamp := state.Timestamp.UTC().Format(time.RFC3339)
	message, err := renderMessage(state.Status, timestamp, state.Diagnostic)
	if err != nil {
		return "", err
	}
	if err := sender.Send(ctx, message); err != nil {
		return "", fmt.Errorf("send %s notification: %w", state.Status, err)
	}
	sentBody := issueBody(timestamp, state.Status, state.Diagnostic, "sent")
	if err := issues.Update(ctx, item.Number, sentBody); err != nil {
		return "", fmt.Errorf("persist %s sent state: %w", state.Status, err)
	}
	return sentBody, nil
}

func transitionAndDeliver(ctx context.Context, issues issueStore, sender messageSender, item *issue, timestamp, status string, category diagnosticCategory, closeAfter bool) error {
	pending := issueBody(timestamp, status, category, "pending")
	if err := issues.Update(ctx, item.Number, pending); err != nil {
		return fmt.Errorf("persist %s pending state: %w", status, err)
	}
	state, _ := parseIssueBody(pending)
	sent, err := deliverPending(ctx, issues, sender, item, state)
	if err != nil {
		return err
	}
	if closeAfter {
		return closeIssueBody(ctx, issues, item.Number, sent)
	}
	return nil
}

func createAndDeliver(ctx context.Context, issues issueStore, sender messageSender, timestamp string, category diagnosticCategory) error {
	pending := issueBody(timestamp, "firing", category, "pending")
	item, err := issues.Create(ctx, pending)
	if err != nil {
		return fmt.Errorf("create monitor issue: %w", err)
	}
	state, _ := parseIssueBody(pending)
	_, err = deliverPending(ctx, issues, sender, item, state)
	return err
}

func closeIssue(ctx context.Context, issues issueStore, item *issue) error {
	return closeIssueBody(ctx, issues, item.Number, item.Body)
}
func closeIssueBody(ctx context.Context, issues issueStore, number int, body string) error {
	if err := issues.Close(ctx, number, body); err != nil {
		return fmt.Errorf("close monitor issue: %w", err)
	}
	return nil
}

func hasMonitorLabel(labels []label) bool {
	return len(labels) == 1 && labels[0].Name == issueLabel
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
