package domain

import (
	"strings"
	"testing"
)

func TestCandidateCommandValidate(t *testing.T) {
	valid := CandidateCommand{ActorID: "admin-1", ActorType: ActorTypeAdmin, Reason: "approved", IdempotencyKey: "key-1", ExpectedStateVersion: 3}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid command, got %v", err)
	}
	cases := []struct {
		name string
		mut  func(*CandidateCommand)
	}{
		{"empty actor", func(c *CandidateCommand) { c.ActorID = "" }},
		{"whitespace actor", func(c *CandidateCommand) { c.ActorID = "  " }},
		{"non-admin actor", func(c *CandidateCommand) { c.ActorType = ActorTypeSystem }},
		{"empty reason", func(c *CandidateCommand) { c.Reason = "" }},
		{"empty idempotency key", func(c *CandidateCommand) { c.IdempotencyKey = "" }},
		{"zero state version", func(c *CandidateCommand) { c.ExpectedStateVersion = 0 }},
		{"negative state version", func(c *CandidateCommand) { c.ExpectedStateVersion = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := valid
			tc.mut(&cmd)
			if err := cmd.Validate(); err != ErrInvalidCandidateCommand {
				t.Errorf("expected ErrInvalidCandidateCommand, got %v", err)
			}
		})
	}
}

func TestCandidateCommandFingerprintDeterministicAndSensitive(t *testing.T) {
	base := CandidateCommand{ActorID: "admin-1", ActorType: ActorTypeAdmin, Reason: "ok", IdempotencyKey: "k", ExpectedStateVersion: 2}
	if fp := base.Fingerprint(); fp != base.Fingerprint() {
		t.Error("fingerprint must be deterministic")
	}
	changed := base
	changed.ExpectedStateVersion = 3
	if base.Fingerprint() == changed.Fingerprint() {
		t.Error("state version must affect fingerprint")
	}
}

func TestExperimentCommandValidateAdminRequired(t *testing.T) {
	cmd := ExperimentCommand{ActorID: "sys-1", ActorType: ActorTypeSystem, Reason: "auto", IdempotencyKey: "k", ExpectedStateVersion: 1}
	err := cmd.Validate()
	if err == nil || !strings.Contains(err.Error(), "admin actor") {
		t.Errorf("expected admin actor error, got %v", err)
	}
}

func TestExperimentCommandValidateWhitespaceFields(t *testing.T) {
	// 极端情况：TrimSpace 后为空的值同样拒绝。
	cmd := ExperimentCommand{ActorID: " ", ActorType: " ", Reason: "\t", IdempotencyKey: "\n", ExpectedStateVersion: 0}
	if err := cmd.Validate(); err == nil {
		t.Error("expected whitespace-only fields to fail")
	}
}

func TestMetricsFingerprintDeterministicAndSensitive(t *testing.T) {
	m := StageMetrics{Samples: 120, ObservedMinutes: 90, QualityImprovement: 0.5, QualitySignificant: true, CostRegression: 0.02}
	if fp := MetricsFingerprint(m); fp != MetricsFingerprint(m) {
		t.Error("fingerprint must be deterministic")
	}
	other := m
	other.Samples = 121
	if MetricsFingerprint(m) == MetricsFingerprint(other) {
		t.Error("different metrics must yield different fingerprints")
	}
}

func TestMetricsFingerprintEmpty(t *testing.T) {
	// 极端情况：零值 metrics 仍是合法 fingerprint（JSON 序列化不能 panic）。
	hash := MetricsFingerprint(StageMetrics{})
	if len(hash) != 64 {
		t.Errorf("expected 64 hex chars, got %d", len(hash))
	}
}
