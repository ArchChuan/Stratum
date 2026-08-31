package persistence

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func TestSanitizeValueRedactsSensitiveEvaluationOutput(t *testing.T) {
	value := map[string]any{
		"result": "ok",
		"token":  "secret-token",
		"nested": map[string]any{"api_key": "secret-key", "count": 2},
		"text":   "authorization=Bearer secret-value",
	}

	encoded, err := json.Marshal(sanitizeValue(value))
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{"secret-token", "secret-key", "secret-value"} {
		if strings.Contains(text, secret) {
			t.Fatalf("sanitized output leaked %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "[REDACTED]") || !strings.Contains(text, `"result":"ok"`) {
		t.Fatalf("unexpected sanitized output: %s", text)
	}
}

// TestPgRunRepository_dimensionsFailureReasonRoundTrip 验证 SaveRun 写入
// dimensions/failure_reason 后 GetRun 能读回相等（spec §6.2 多维分数与失败归因）。
func TestPgRunRepository_dimensionsFailureReasonRoundTrip(t *testing.T) {
	writeMock := newMockRepo(t)
	readMock := newMockRepo(t)
	now := time.Now()
	run := domain.EvalRun{
		ID:              "run-rt",
		Resource:        domain.ResourceRef{Kind: "prompt", ResourceID: "r-1", RevisionID: "rev-1"},
		SuiteRevisionID: "s-1",
		Passed:          true,
		TotalCases:      1,
		PassedCases:     1,
		Metrics:         map[string]any{"pass_rate": 1.0},
		CreatedAt:       now,
		Results: []domain.EvalCaseResult{
			{
				ID: "case-rt", CaseID: "case-1", Passed: true,
				Actual:        map[string]any{"ok": true},
				Message:       "m",
				TraceID:       "tr-1",
				Tokens:        5,
				CostUSD:       0.1,
				DurationMs:    2,
				Dimensions:    []domain.DimensionScore{{Name: "correctness", Score: 1, Passed: true, Reason: "ok"}},
				FailureReason: "assert failed",
				TraceEvidence: &domain.ObservedTraceEvidence{
					CostUSD: 0.05, LatencyMs: 250, Success: false, SecurityViolation: true,
					ToolCallCount: 4, ToolErrorCount: 2,
				},
			},
		},
	}

	// SaveRun 侧：dimensions 序列化为 JSON 数组，failure_reason 原样写入。
	expectTenantTx(writeMock)
	writeMock.ExpectExec("INSERT INTO eval_runs").
		WithArgs("run-rt", "prompt", "r-1", "rev-1", "s-1", true, 1, 1, `{"pass_rate":1}`, now).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	writeMock.ExpectExec("INSERT INTO eval_case_results").
		WithArgs("case-rt", "run-rt", "case-1", true, `{"ok":true}`, "m", "", "tr-1", 5, 0.1, 2,
			`[{"name":"correctness","score":1,"passed":true,"reason":"ok"}]`, "assert failed",
			`{"cost_usd":0.05,"latency_ms":250,"success":false,`+
				`"security_violation":true,"tool_call_count":4,"tool_error_count":2}`).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	writeMock.ExpectCommit()
	writeRepo := &PgRunRepository{pool: writeMock}
	require.NoError(t, writeRepo.SaveRun(context.Background(), "t1", run))
	require.NoError(t, writeMock.ExpectationsWereMet())

	// GetRun 侧：读回相等。
	expectTenantTx(readMock)
	readMock.ExpectQuery("SELECT id, resource_kind, resource_id, revision_id, suite_revision_id").
		WithArgs("run-rt").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "revision_id", "suite_revision_id",
			"passed", "total_cases", "passed_cases", "metrics", "created_at",
		}).AddRow("run-rt", "prompt", "r-1", "rev-1", "s-1", true, 1, 1, []byte(`{"pass_rate":1}`), now))
	readMock.ExpectQuery("SELECT case_id, passed, actual_output").
		WithArgs("run-rt").
		WillReturnRows(pgxmock.NewRows([]string{
			"case_id", "passed", "actual_output", "message", "error_message", "trace_id",
			"tokens", "cost_usd", "duration_ms", "dimensions", "failure_reason", "trace_evidence",
		}).AddRow("case-1", true, []byte(`{"ok":true}`), "m", "", "tr-1", 5, 0.1, 2,
			[]byte(`[{"name":"correctness","score":1,"passed":true,"reason":"ok"}]`), "assert failed",
			[]byte(`{"cost_usd":0.05,"latency_ms":250,"success":false,`+
				`"security_violation":true,"tool_call_count":4,"tool_error_count":2}`)))
	readMock.ExpectCommit()
	readRepo := &PgRunRepository{pool: readMock}
	got, found, err := readRepo.GetRun(context.Background(), "t1", "run-rt")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, got.Results, 1)
	require.Equal(t, run.Results[0].Dimensions, got.Results[0].Dimensions)
	require.Equal(t, run.Results[0].FailureReason, got.Results[0].FailureReason)
	require.Equal(t, run.Results[0].TraceEvidence, got.Results[0].TraceEvidence)
	require.NoError(t, readMock.ExpectationsWereMet())
}
