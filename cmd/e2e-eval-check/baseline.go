package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// fingerprint captures the point's configuration identity. Any change marks
// the run non-comparable and forces an explicit human decision.
type fingerprint struct {
	Hash         string `json:"hash"`
	ProviderHash string `json:"provider_hash,omitempty"`
}

// baseline is the recorded reference for one point. accepted_regressions is
// the authoritative, durable store for explicitly accepted regressions.
type baseline struct {
	RecordedCommit      string               `json:"recorded_commit"`
	RecordedAt          string               `json:"recorded_at"`
	Kind                string               `json:"kind"`
	Point               string               `json:"point"`
	Fingerprint         fingerprint          `json:"fingerprint"`
	Aggregate           aggregate            `json:"aggregate"`
	AcceptedRegressions []acceptedRegression `json:"accepted_regressions"`
}

// acceptedRegression records one explicitly accepted regression with a reason.
type acceptedRegression struct {
	Metric   string  `json:"metric"`
	Baseline float64 `json:"baseline"`
	Run      float64 `json:"run"`
	Commit   string  `json:"commit"`
	Reason   string  `json:"reason"`
}

// baselineDelta summarizes the run vs baseline for the report.
type baselineDelta struct {
	RunPassRate   float64 `json:"run_pass_rate"`
	BasePassRate  float64 `json:"base_pass_rate"`
	PassRateDelta float64 `json:"pass_rate_delta"`
}

// loadBaseline reads a baseline file. A missing file is not an error: it means
// first recording. A corrupt file is an explicit error.
func loadBaseline(path string) (*baseline, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read baseline %s: %w", path, err)
	}
	var b baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("corrupt baseline %s: %w", path, err)
	}
	return &b, nil
}

// writeBaseline persists a baseline atomically (write temp + rename).
func writeBaseline(path string, b baseline) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("mkdir baseline dir: %w", err)
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("encode baseline: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write baseline temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename baseline: %w", err)
	}
	return nil
}
