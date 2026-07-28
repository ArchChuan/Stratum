package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/platform/alerting"
	"github.com/stretchr/testify/require"
)

func TestMonitorHealthyWithoutIssueIsSilent(t *testing.T) {
	probe := &stubProbe{results: []probeResult{{}}}
	issues := &stubIssues{}
	sender := &stubSender{}

	err := monitor(context.Background(), probe, issues, sender, fixedNow)

	require.NoError(t, err)
	require.Equal(t, 1, probe.calls)
	require.Empty(t, issues.created)
	require.Empty(t, issues.closed)
	require.Empty(t, sender.messages)
}

func TestMonitorFailureAttemptsThreeTimesThenCreatesIssueAndFiresOnce(t *testing.T) {
	probe := &stubProbe{results: []probeResult{
		{category: diagnosticTransport},
		{category: diagnosticHTTPStatus},
		{category: diagnosticContract},
	}}
	issues := &stubIssues{}
	sender := &stubSender{}

	err := monitor(context.Background(), probe, issues, sender, fixedNow)

	require.NoError(t, err)
	require.Equal(t, 3, probe.calls)
	require.Len(t, issues.created, 1)
	require.Contains(t, issues.created[0], "status: firing")
	require.Contains(t, issues.created[0], "diagnostic: contract")
	require.Contains(t, issues.created[0], "notification: pending")
	require.NotContains(t, issues.created[0], "secret")
	require.Len(t, sender.messages, 1)
	require.Contains(t, sender.messages[0].Card.Header.Title.Content, "FIRING")
	require.Len(t, issues.bodies, 1)
	require.Contains(t, issues.bodies[0], "notification: sent")
}

func TestMonitorFailureWithOpenIssueUpdatesTimestampWithoutDuplicateNotification(t *testing.T) {
	probe := &stubProbe{results: []probeResult{{category: diagnosticTransport}}}
	issues := &stubIssues{open: firingIssue(42, "sent")}
	sender := &stubSender{}

	err := monitor(context.Background(), probe, issues, sender, fixedNow)

	require.NoError(t, err)
	require.Equal(t, 3, probe.calls)
	require.Empty(t, issues.created)
	require.Equal(t, []int{42}, issues.updated)
	require.Empty(t, sender.messages)
}

func TestMonitorHealthyWithOpenIssueClosesAndSendsResolvedOnce(t *testing.T) {
	probe := &stubProbe{results: []probeResult{{}}}
	issues := &stubIssues{open: firingIssue(42, "sent")}
	sender := &stubSender{}

	err := monitor(context.Background(), probe, issues, sender, fixedNow)

	require.NoError(t, err)
	require.Equal(t, []int{42}, issues.closed)
	require.Len(t, sender.messages, 1)
	require.Contains(t, sender.messages[0].Card.Header.Title.Content, "RESOLVED")
}

func TestMonitorGitHubStateFailureFailsClosedWithoutRecoveryClaim(t *testing.T) {
	probe := &stubProbe{results: []probeResult{{}}}
	issues := &stubIssues{findErr: errors.New("github unavailable")}
	sender := &stubSender{}

	err := monitor(context.Background(), probe, issues, sender, fixedNow)

	require.Error(t, err)
	require.Empty(t, issues.closed)
	require.Empty(t, sender.messages)
}

func TestMonitorResolvedDeliveryFailureLeavesIssueOpen(t *testing.T) {
	probe := &stubProbe{results: []probeResult{{}}}
	issues := &stubIssues{open: firingIssue(42, "sent")}
	sender := &stubSender{err: errors.New("feishu unavailable")}

	err := monitor(context.Background(), probe, issues, sender, fixedNow)

	require.Error(t, err)
	require.Empty(t, issues.closed)
	require.Len(t, sender.messages, 1)
}

func TestMonitorFiringSendFailureRemainsPendingAndRetriesNextRun(t *testing.T) {
	probe := &stubProbe{results: []probeResult{{category: diagnosticTransport}}}
	issues := &stubIssues{}
	sender := &stubSender{err: errors.New("feishu unavailable")}

	require.Error(t, monitor(context.Background(), probe, issues, sender, fixedNow))
	require.Len(t, issues.created, 1)
	require.Contains(t, issues.created[0], "notification: pending")
	require.Empty(t, issues.bodies)

	issues.open = firingIssue(101, "pending")
	sender.err = nil
	probe.calls = 0
	require.NoError(t, monitor(context.Background(), probe, issues, sender, fixedNow))
	require.Len(t, sender.messages, 2)
	require.Contains(t, issues.bodies[len(issues.bodies)-1], "notification: sent")
}

func TestMonitorCloseFailureThenHealthyRerunDoesNotDuplicateResolved(t *testing.T) {
	probe := &stubProbe{results: []probeResult{{}}}
	issues := &stubIssues{open: firingIssue(42, "sent"), closeErr: errors.New("github unavailable")}
	sender := &stubSender{}

	require.Error(t, monitor(context.Background(), probe, issues, sender, fixedNow))
	require.Len(t, sender.messages, 1)
	require.Contains(t, issues.bodies[len(issues.bodies)-1], "status: resolved")
	require.Contains(t, issues.bodies[len(issues.bodies)-1], "notification: sent")

	issues.open = resolvedIssue(42, "sent")
	issues.closeErr = nil
	require.NoError(t, monitor(context.Background(), probe, issues, sender, fixedNow))
	require.Len(t, sender.messages, 1)
	require.Equal(t, []int{42}, issues.closed)
}

func TestMonitorTitleMismatchFailsClosed(t *testing.T) {
	issues := &stubIssues{open: &issue{Number: 42, Title: "unrelated", Body: issueBody(
		fixedNow().Format(time.RFC3339), "firing", diagnosticTransport, "sent",
	)}}
	sender := &stubSender{}

	require.Error(t, monitor(context.Background(), &stubProbe{results: []probeResult{{}}}, issues, sender, fixedNow))
	require.Empty(t, sender.messages)
	require.Empty(t, issues.closed)
}

func TestMonitorMalformedIssueBodyFailsClosed(t *testing.T) {
	issues := &stubIssues{open: &issue{Number: 42, Title: issueTitle, Body: "status: firing\nraw: unexpected\n"}}
	sender := &stubSender{}

	require.Error(t, monitor(context.Background(), &stubProbe{results: []probeResult{{}}}, issues, sender, fixedNow))
	require.Empty(t, sender.messages)
	require.Empty(t, issues.closed)
}

func TestMonitorRecoveryPendingStateUpdateFailureDoesNotSend(t *testing.T) {
	issues := &stubIssues{open: firingIssue(42, "sent"), updateErr: errors.New("github unavailable")}
	sender := &stubSender{}

	require.Error(t, monitor(context.Background(), &stubProbe{results: []probeResult{{}}}, issues, sender, fixedNow))
	require.Empty(t, sender.messages)
	require.Empty(t, issues.closed)
}

func TestMonitorFiringSentStateUpdateFailureExitsNonzeroWithPendingIssue(t *testing.T) {
	issues := &stubIssues{updateErr: errors.New("github unavailable")}
	sender := &stubSender{}

	err := monitor(context.Background(), &stubProbe{results: []probeResult{{category: diagnosticTransport}}}, issues, sender, fixedNow)

	require.Error(t, err)
	require.Len(t, sender.messages, 1)
	require.Len(t, issues.created, 1)
	require.Contains(t, issues.created[0], "notification: pending")
	require.Empty(t, issues.bodies)
}

func TestHTTPProbeRequiresExactStableContractAndDoesNotLeakBody(t *testing.T) {
	probe, server := newProbeTestServer(t, 200, `{"service":"Stratum","status":"degraded","secret":"do-not-log"}`)
	defer server.Close()

	category := probe.Check(context.Background())

	require.Equal(t, diagnosticContract, category)
	require.NotContains(t, string(category), "do-not-log")
}

func TestHTTPProbeRejectsUnknownHealthFields(t *testing.T) {
	probe, server := newProbeTestServer(t, 200, `{"service":"Stratum","status":"ok","extra":"unexpected"}`)
	defer server.Close()

	require.Equal(t, diagnosticContract, probe.Check(context.Background()))
}

type stubProbe struct {
	results []probeResult
	calls   int
}

func (s *stubProbe) Check(context.Context) diagnosticCategory {
	index := s.calls
	s.calls++
	if index >= len(s.results) {
		index = len(s.results) - 1
	}
	return s.results[index].category
}

type probeResult struct{ category diagnosticCategory }

type stubIssues struct {
	open      *issue
	findErr   error
	created   []string
	updated   []int
	bodies    []string
	closed    []int
	closeErr  error
	updateErr error
}

func (s *stubIssues) FindOpen(context.Context) (*issue, error) { return s.open, s.findErr }
func (s *stubIssues) Create(_ context.Context, body string) (*issue, error) {
	s.created = append(s.created, body)
	return &issue{Number: 101, Title: issueTitle, Body: body}, nil
}
func (s *stubIssues) Update(_ context.Context, number int, body string) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	s.updated = append(s.updated, number)
	s.bodies = append(s.bodies, body)
	return nil
}
func (s *stubIssues) Close(_ context.Context, number int, _ string) error {
	if s.closeErr != nil {
		return s.closeErr
	}
	s.closed = append(s.closed, number)
	return nil
}

type stubSender struct {
	messages []alerting.FeishuMessage
	err      error
}

func (s *stubSender) Send(_ context.Context, message alerting.FeishuMessage) error {
	s.messages = append(s.messages, message)
	return s.err
}

func fixedNow() time.Time { return time.Date(2026, 7, 29, 1, 2, 3, 0, time.UTC) }

func firingIssue(number int, notification string) *issue {
	return &issue{Number: number, Title: issueTitle, Body: issueBody(
		fixedNow().Format(time.RFC3339), "firing", diagnosticTransport, notification,
	)}
}

func resolvedIssue(number int, notification string) *issue {
	return &issue{Number: number, Title: issueTitle, Body: issueBody(
		fixedNow().Format(time.RFC3339), "resolved", diagnosticNone, notification,
	)}
}

func newProbeTestServer(t *testing.T, status int, body string) (*httpProbe, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	return &httpProbe{client: server.Client(), url: server.URL}, server
}
