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
	require.NotContains(t, issues.created[0], "secret")
	require.Len(t, sender.messages, 1)
	require.Contains(t, sender.messages[0].Card.Header.Title.Content, "FIRING")
}

func TestMonitorFailureWithOpenIssueUpdatesTimestampWithoutDuplicateNotification(t *testing.T) {
	probe := &stubProbe{results: []probeResult{{category: diagnosticTransport}}}
	issues := &stubIssues{open: &issue{Number: 42}}
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
	issues := &stubIssues{open: &issue{Number: 42}}
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
	issues := &stubIssues{open: &issue{Number: 42}}
	sender := &stubSender{err: errors.New("feishu unavailable")}

	err := monitor(context.Background(), probe, issues, sender, fixedNow)

	require.Error(t, err)
	require.Empty(t, issues.closed)
	require.Len(t, sender.messages, 1)
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
	open    *issue
	findErr error
	created []string
	updated []int
	closed  []int
}

func (s *stubIssues) FindOpen(context.Context) (*issue, error) { return s.open, s.findErr }
func (s *stubIssues) Create(_ context.Context, body string) error {
	s.created = append(s.created, body)
	return nil
}
func (s *stubIssues) Update(_ context.Context, number int, _ string) error {
	s.updated = append(s.updated, number)
	return nil
}
func (s *stubIssues) Close(_ context.Context, number int, _ string) error {
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

func newProbeTestServer(t *testing.T, status int, body string) (*httpProbe, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	return &httpProbe{client: server.Client(), url: server.URL}, server
}
