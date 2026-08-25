package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadBaselineMissing(t *testing.T) {
	got, err := loadBaseline(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing baseline must not error, got %v", err)
	}
	if got != nil {
		t.Fatalf("missing baseline must return nil, got %+v", got)
	}
}

func TestLoadBaselineCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	writeTestFile(t, path, "{not json")
	_, err := loadBaseline(path)
	if err == nil || !strings.Contains(err.Error(), "corrupt baseline") {
		t.Fatalf("expected corrupt baseline error, got %v", err)
	}
}

func TestWriteBaselineAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baselines", "p1.json")
	b := baseline{
		RecordedCommit:      "abc123",
		RecordedAt:          "2026-08-26T00:00:00Z",
		Kind:                "knowledge",
		Point:               "p1",
		Fingerprint:         fingerprint{Hash: "h1", ProviderHash: "p1"},
		Aggregate:           aggregate{CaseCount: 2, PassRate: 1.0},
		AcceptedRegressions: []acceptedRegression{{Metric: "recall", Baseline: 0.8, Run: 0.7, Commit: "abc123", Reason: "drift"}},
	}
	if err := writeBaseline(path, b); err != nil {
		t.Fatalf("writeBaseline: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("baseline file not written: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file must be removed after atomic rename")
	}
	got, err := loadBaseline(path)
	if err != nil {
		t.Fatalf("loadBaseline: %v", err)
	}
	if got == nil {
		t.Fatal("expected a baseline after write")
	}
	if got.RecordedCommit != "abc123" || got.Kind != "knowledge" || got.Point != "p1" {
		t.Fatalf("round-trip metadata mismatch: %+v", got)
	}
	if got.Fingerprint.Hash != "h1" || got.Fingerprint.ProviderHash != "p1" {
		t.Fatalf("round-trip fingerprint mismatch: %+v", got.Fingerprint)
	}
	if got.Aggregate.PassRate != 1.0 || got.Aggregate.CaseCount != 2 {
		t.Fatalf("round-trip aggregate mismatch: %+v", got.Aggregate)
	}
	if len(got.AcceptedRegressions) != 1 || got.AcceptedRegressions[0].Metric != "recall" {
		t.Fatalf("round-trip accepted regressions mismatch: %+v", got.AcceptedRegressions)
	}
}
