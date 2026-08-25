package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectPointPathsNestedLayout(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeTestFile(t, filepath.Join("test", "e2e", "knowledge", "retrieval", "points", "retrieval.yaml"), "kind: knowledge\n")
	writeTestFile(t, filepath.Join("test", "e2e", "knowledge", "retrieval", "golden", "cases.yaml"), "cases:\n")
	writeTestFile(t, filepath.Join("test", "e2e", "mcp", "points", "weather-mcp.yaml"), "kind: mcp\n")

	got, err := collectPointPaths(filepath.Join("test", "e2e", "knowledge"))
	if err != nil {
		t.Fatalf("collectPointPaths: %v", err)
	}
	want := []string{filepath.Join("test", "e2e", "knowledge", "retrieval", "points", "retrieval.yaml")}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("collectPointPaths = %v, want %v", got, want)
	}
}

func TestCollectPointPathsMissingRootYieldsEmpty(t *testing.T) {
	got, err := collectPointPaths(filepath.Join(t.TempDir(), "no-such-kind"))
	if err != nil {
		t.Fatalf("collectPointPaths: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("collectPointPaths = %v, want empty", got)
	}
}

func TestResolvePointPathNested(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeTestFile(t, filepath.Join("test", "e2e", "knowledge", "retrieval", "points", "retrieval.yaml"), "kind: knowledge\n")
	got, err := resolvePointPath("knowledge", "retrieval")
	if err != nil {
		t.Fatalf("resolvePointPath: %v", err)
	}
	want := filepath.Join("test", "e2e", "knowledge", "retrieval", "points", "retrieval.yaml")
	if got != want {
		t.Fatalf("resolvePointPath = %q, want %q", got, want)
	}
}

func TestResolvePointPathNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if _, err := resolvePointPath("skill", "does-not-exist"); err == nil {
		t.Fatal("resolvePointPath must fail for a missing point")
	}
}

func TestResolvePointPathAmbiguous(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeTestFile(t, filepath.Join("test", "e2e", "knowledge", "a", "points", "p.yaml"), "kind: knowledge\n")
	writeTestFile(t, filepath.Join("test", "e2e", "knowledge", "b", "points", "p.yaml"), "kind: knowledge\n")
	if _, err := resolvePointPath("knowledge", "p"); err == nil {
		t.Fatal("resolvePointPath must fail when the key matches multiple nested points")
	}
}

func TestSelfCheckNestedKnowledge(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeTestFile(t, filepath.Join("test", "e2e", "knowledge", "retrieval", "points", "retrieval.yaml"),
		"kind: knowledge\npoint: retrieval\ngolden: ../golden\nbaseline: ../baselines/retrieval.json\n")
	writeTestFile(t, filepath.Join("test", "e2e", "knowledge", "retrieval", "golden", "cases.yaml"), "cases:\n")
	writeTestFile(t, filepath.Join("test", "e2e", "knowledge", "retrieval", "baselines", "retrieval.json"), "{}")

	var out bytes.Buffer
	code, err := selfCheck(options{kind: "knowledge"}, &out)
	if err != nil {
		t.Fatalf("selfCheck: %v", err)
	}
	if code != exitPassed {
		t.Fatalf("selfCheck code = %d, want %d\nout:\n%s", code, exitPassed, out.String())
	}
	if !strings.Contains(out.String(), "POINT OK") {
		t.Fatalf("output missing POINT OK:\n%s", out.String())
	}
}

func TestSelfCheck(t *testing.T) {
	cases := []struct {
		name        string
		kind        string
		points      map[string]string
		goldenFiles map[string]string
		wantCode    int
		wantOut     string
		notWantOut  string
	}{
		{
			name: "missing golden fails self-check",
			kind: "skill",
			points: map[string]string{
				"p1.yaml": "kind: skill\npoint: p1\ngolden: golden/cases.yaml\nbaseline: baselines/p1.json\n",
			},
			wantCode:   exitFailed,
			wantOut:    "GOLDEN FAIL",
			notWantOut: "POINT OK",
		},
		{
			name: "point with existing golden passes",
			kind: "agent",
			points: map[string]string{
				"p1.yaml": "kind: agent\npoint: p1\ngolden: golden/cases.yaml\nbaseline: baselines/p1.json\n",
			},
			goldenFiles: map[string]string{
				"golden/cases.yaml": "cases:\n",
			},
			wantCode: exitPassed,
			wantOut:  "POINT OK",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			for name, content := range tc.points {
				writeTestFile(t, filepath.Join("test", "e2e", tc.kind, "points", name), content)
			}
			for name, content := range tc.goldenFiles {
				writeTestFile(t, filepath.Join("test", "e2e", tc.kind, "points", name), content)
			}
			var out bytes.Buffer
			code, err := selfCheck(options{kind: tc.kind}, &out)
			if err != nil {
				t.Fatalf("selfCheck: %v", err)
			}
			if code != tc.wantCode {
				t.Fatalf("selfCheck code = %d, want %d\nout:\n%s", code, tc.wantCode, out.String())
			}
			if !strings.Contains(out.String(), tc.wantOut) {
				t.Fatalf("output missing %q:\n%s", tc.wantOut, out.String())
			}
			if tc.notWantOut != "" && strings.Contains(out.String(), tc.notWantOut) {
				t.Fatalf("output unexpectedly contains %q:\n%s", tc.notWantOut, out.String())
			}
		})
	}
}
