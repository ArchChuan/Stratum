package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusOf(t *testing.T) {
	cases := []struct {
		name string
		code int
		want string
	}{
		{name: "passed", code: exitPassed, want: statusPassed},
		{name: "failed", code: exitFailed, want: statusFailed},
		{name: "infra failed", code: exitInfraFailed, want: statusInfraFail},
		{name: "unknown exit code maps to failed", code: 99, want: statusFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusOf(tc.code); got != tc.want {
				t.Fatalf("statusOf(%d) = %q, want %q", tc.code, got, tc.want)
			}
		})
	}
}

func TestWriteReportNormalizesNilSlices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	r := report{
		Version:       reportSchemaVersion,
		Kind:          "mcp",
		Point:         "p1",
		Status:        statusPassed,
		GeneratedAt:   nowUTC(),
		Snapshot:      map[string]any{},
		Aggregate:     aggregate{CaseCount: 0, PassRate: 0},
		NonComparable: false,
	}
	if err := writeReport(path, r); err != nil {
		t.Fatalf("writeReport: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	for _, key := range []string{"cases", "warnings", "residual_entities", "evidence", "accepted_regressions"} {
		if m[key] == nil {
			t.Fatalf("report field %q must serialize as [] not null", key)
		}
	}
}

func TestPrintSummary(t *testing.T) {
	r := report{
		Kind:                "knowledge",
		Point:               "p1",
		Status:              statusPassed,
		Aggregate:           aggregate{CaseCount: 2, PassRate: 0.75},
		Warnings:            []warning{{ID: warnRegression, Level: warnStrong, Message: "recall regressed"}},
		AcceptedRegressions: []acceptedRegression{{Metric: "mrr", Baseline: 0.8, Run: 0.7, Reason: "llm drift"}},
	}
	var buf bytes.Buffer
	printSummary(&buf, r)
	s := buf.String()
	for _, want := range []string{
		"kind=knowledge point=p1 status=passed",
		"cases=2 pass_rate=0.7500",
		"warn[strong] regression: recall regressed",
		"accepted_regression mrr baseline=0.8000 run=0.7000 reason=llm drift",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("printSummary output missing %q:\n%s", want, s)
		}
	}
}
