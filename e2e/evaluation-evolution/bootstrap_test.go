//go:build ignore

package main

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestEvidencePollingRetriesOnlyExplicitTransientStatuses(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusNotFound, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		if !isTransientEvidenceStatus(status) {
			t.Fatalf("status %d should be retryable", status)
		}
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest,
		http.StatusConflict, http.StatusInternalServerError} {
		if isTransientEvidenceStatus(status) {
			t.Fatalf("status %d must fail immediately", status)
		}
	}
}

func TestEvidencePollingSummaryContainsOnlyBoundedCounts(t *testing.T) {
	t.Parallel()

	stats := evidencePollingStats{
		statusCounts:      map[int]int{http.StatusOK: 2, http.StatusServiceUnavailable: 3},
		transportTimeouts: 1,
		emptyOK:           2,
		lastStatus:        "status=200-empty",
	}
	want := "statuses=200:2,503:3 transport-timeouts=1 empty-200=2 last=status=200-empty"
	if got := stats.summary(); got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}

func TestTenantSchemaNamePreservesTenantID(t *testing.T) {
	t.Parallel()

	tenantID := "12345678-1234-1234-1234-123456789abc"
	want := "tenant_12345678-1234-1234-1234-123456789abc"
	if got := tenantSchemaName(tenantID); got != want {
		t.Fatalf("tenantSchemaName(%q) = %q, want %q", tenantID, got, want)
	}
}

func TestQuoteIdentifierSupportsTenantUUIDSchema(t *testing.T) {
	t.Parallel()

	identifier := "tenant_12345678-1234-1234-1234-123456789abc"
	want := `"tenant_12345678-1234-1234-1234-123456789abc"`
	if got := quoteIdentifier(identifier); got != want {
		t.Fatalf("quoteIdentifier(%q) = %q, want %q", identifier, got, want)
	}
}

func TestKnowledgeOutagePreservesMilvusPublishedPort(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("bootstrap.go")
	if err != nil {
		t.Fatalf("read bootstrap source: %v", err)
	}
	text := string(source)
	for _, forbidden := range []string{
		`exec.Command("docker", "stop", milvusContainer)`,
		`exec.Command("docker", "start", milvusContainer)`,
		`exec.Command("docker", "pause", milvusContainer)`,
		`exec.Command("docker", "unpause", milvusContainer)`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("Knowledge outage must preserve the Milvus host mapping; found %s", forbidden)
		}
	}
	for _, required := range []string{
		`setMilvusProxyEnabled(false)`,
		`setMilvusProxyEnabled(true)`,
		`assertMilvusContainerHealthy(milvusContainer)`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("Knowledge outage is missing %s", required)
		}
	}
}

func TestSanitizeContainerDiagnosticRedactsCredentials(t *testing.T) {
	t.Parallel()

	input := "health error password=hunter2 token: abc Bearer signed-value api_key=qwerty"
	got := sanitizeContainerDiagnostic(input)
	for _, secret := range []string{"hunter2", "abc", "signed-value", "qwerty"} {
		if strings.Contains(got, secret) {
			t.Fatalf("diagnostic retained credential %q: %q", secret, got)
		}
	}
	if !strings.Contains(got, "health error") {
		t.Fatalf("diagnostic removed useful health evidence: %q", got)
	}
}

func TestHarnessHasBoundedCleanupAndOpikFailureDiagnostics(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("../../scripts/e2e/evaluation-evolution.sh")
	if err != nil {
		t.Fatalf("read E2E harness: %v", err)
	}
	text := string(source)
	for _, forbidden := range []string{
		`wait "$frontend_pid"`, `wait "$backend_pid"`, `wait "$mcp_pid"`, `wait "$llm_pid"`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("cleanup contains unbounded wait: %s", forbidden)
		}
	}
	for _, required := range []string{
		"stop_process_group", "bounded_compose_down", "opik_readiness_diagnostics", "poll_opik",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("E2E harness is missing %s", required)
		}
	}
}

func TestHarnessStartsMilvusAfterOpikAndBeforeStratum(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("../../scripts/e2e/evaluation-evolution.sh")
	if err != nil {
		t.Fatalf("read E2E harness: %v", err)
	}
	text := string(source)
	base := strings.Index(text, `up -d --wait postgres nats redis minio etcd otel`)
	opikReady := strings.Index(text, "\npoll_opik\n")
	milvus := strings.Index(text, `up -d --wait milvus`)
	proxy := strings.Index(text, `tcp-proxy.go`)
	server := strings.Index(text, `exec go run ./cmd/server`)
	if base < 0 || opikReady < 0 || milvus < 0 || proxy < 0 || server < 0 {
		t.Fatalf("startup markers missing: base=%d opik=%d milvus=%d proxy=%d server=%d",
			base, opikReady, milvus, proxy, server)
	}
	if !(base < opikReady && opikReady < milvus && milvus < proxy && proxy < server) {
		t.Fatalf("startup order invalid: base=%d opik=%d milvus=%d proxy=%d server=%d",
			base, opikReady, milvus, proxy, server)
	}
}

func TestEvidencePollingBudgetCoversLoadedOpikIngestion(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("bootstrap.go")
	if err != nil {
		t.Fatalf("read bootstrap helper: %v", err)
	}
	if !strings.Contains(string(source), "const pollingBudget = 90 * time.Second") {
		t.Fatal("exact Opik evidence polling must retain the reviewed 90 second total budget")
	}
}

func TestHarnessRefreshesCollectorMetricsPortAfterNetworkAttach(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("../../scripts/e2e/evaluation-evolution.sh")
	if err != nil {
		t.Fatalf("read E2E harness: %v", err)
	}
	text := string(source)
	attach := strings.Index(text, `docker network connect "${project}-opik_default" "$otel_cid"`)
	if attach < 0 {
		t.Fatal("Collector is not attached to the Opik network")
	}
	refresh := strings.Index(text[attach:], `port otel 8888`)
	if refresh < 0 {
		t.Fatalf("Collector metrics mapping is not refreshed after Opik network attach: attach=%d refresh=%d",
			attach, refresh)
	}
}
