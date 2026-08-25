package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

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
