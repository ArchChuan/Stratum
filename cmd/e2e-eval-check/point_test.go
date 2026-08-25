package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPoint(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "p1.yaml"), `
kind: knowledge
point: p1
snapshot:
  embedding_model: embedding-3
  query_mode: hybrid
  top_k: 5
  chunk_size: 512
golden: golden/cases.yaml
baseline: baselines/p1.json
`)
	got, err := loadPoint(filepath.Join(dir, "p1.yaml"))
	if err != nil {
		t.Fatalf("loadPoint: %v", err)
	}
	if got.Kind != "knowledge" || got.Key != "p1" {
		t.Fatalf("loadPoint kind/key = %q/%q, want knowledge/p1", got.Kind, got.Key)
	}
	if got.Golden == "" || got.Baseline == "" {
		t.Fatalf("loadPoint must resolve golden and baseline: %+v", got)
	}
}

func TestLoadPointRejectsUnknownKind(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "bad.yaml"), "kind: nope\npoint: bad\n")
	_, err := loadPoint(filepath.Join(dir, "bad.yaml"))
	if err == nil || !strings.Contains(err.Error(), "unsupported kind") {
		t.Fatalf("expected unsupported kind error, got %v", err)
	}
}

func TestResolveRelativePath(t *testing.T) {
	dir := t.TempDir()
	if got, err := resolveRelative(dir, "golden/cases.yaml"); err != nil || got != filepath.Join(dir, "golden/cases.yaml") {
		t.Fatalf("resolveRelative = %q, %v", got, err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
