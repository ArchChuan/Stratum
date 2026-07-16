package process

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProcFSReadIdentity(t *testing.T) {
	identity, err := NewProcFS("testdata/proc").ReadIdentity(42)
	if err != nil {
		t.Fatalf("ReadIdentity: %v", err)
	}
	if identity != (Identity{PID: 42, StartTicks: 98765}) {
		t.Errorf("identity = %+v; want PID 42, start ticks 98765", identity)
	}
}

func TestProcFSReadProcess(t *testing.T) {
	process, warnings, err := NewProcFS("testdata/proc").ReadProcess(42)
	if err != nil {
		t.Fatalf("ReadProcess: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v; want none", warnings)
	}
	want := Process{
		Identity: Identity{PID: 42, StartTicks: 98765},
		PPID:     7, Command: "chrome devtools",
		Args:     []string{"node", "/opt/chrome-devtools-mcp", "--headless"},
		RSSBytes: 1200 * 1024, PSSBytes: 700 * 1024, USSBytes: 400 * 1024,
	}
	if !reflect.DeepEqual(process, want) {
		t.Errorf("process = %+v; want %+v", process, want)
	}
}

func TestProcFSListPIDsSortedAndNumericOnly(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"10", "2", "self", "3x", "-1", "+4"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pids, err := NewProcFS(root).ListPIDs()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pids, []int{2, 10}) {
		t.Errorf("pids = %v; want [2 10]", pids)
	}
}

func TestProcFSMissingRollupIsWarning(t *testing.T) {
	root := copyFixture(t)
	if err := os.Remove(filepath.Join(root, "42", "smaps_rollup")); err != nil {
		t.Fatal(err)
	}
	p, warnings, err := NewProcFS(root).ReadProcess(42)
	if err != nil {
		t.Fatalf("ReadProcess: %v", err)
	}
	if p.RSSBytes != 1200*1024 || p.PSSBytes != 0 || p.USSBytes != 0 {
		t.Errorf("memory = RSS %d PSS %d USS %d", p.RSSBytes, p.PSSBytes, p.USSBytes)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "smaps_rollup") {
		t.Errorf("warnings = %v; want smaps_rollup warning", warnings)
	}
}

func TestProcFSPermissionDeniedRollupIsWarning(t *testing.T) {
	root := copyFixture(t)
	fs := NewProcFS(root)
	fs.readFile = func(path string) ([]byte, error) {
		if filepath.Base(path) == "smaps_rollup" {
			return nil, os.ErrPermission
		}
		return os.ReadFile(path)
	}
	p, warnings, err := fs.ReadProcess(42)
	if err != nil {
		t.Fatalf("ReadProcess: %v", err)
	}
	if p.PSSBytes != 0 || p.USSBytes != 0 || len(warnings) != 1 {
		t.Errorf("process memory = %+v, warnings = %v", p, warnings)
	}
}

func TestProcFSIncludesPrivateHugetlbInUSS(t *testing.T) {
	root := copyFixture(t)
	path := filepath.Join(root, "42", "smaps_rollup")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("Private_Hugetlb:\t50 kB\n")...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	process, _, err := NewProcFS(root).ReadProcess(42)
	if err != nil {
		t.Fatalf("ReadProcess: %v", err)
	}
	if process.USSBytes != 450*1024 {
		t.Errorf("USS = %d; want %d", process.USSBytes, 450*1024)
	}
}

func TestProcFSMalformedInputs(t *testing.T) {
	tests := []struct{ name, file, contents string }{
		{"stat", "stat", "42 (broken) S 7"},
		{"status", "status", "PPid: nope\nVmRSS: 1200 kB\n"},
		{"memory", "smaps_rollup", "Pss: nope kB\nPrivate_Clean: 1 kB\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := copyFixture(t)
			if err := os.WriteFile(filepath.Join(root, "42", tt.file), []byte(tt.contents), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := NewProcFS(root).ReadProcess(42)
			if err == nil {
				t.Fatal("ReadProcess succeeded; want malformed input error")
			}
		})
	}
}

func TestProcFSDetectsPIDReuse(t *testing.T) {
	fs := NewProcFS("testdata/proc")
	statReads := 0
	fs.readFile = func(path string) ([]byte, error) {
		data, err := os.ReadFile(path)
		if filepath.Base(path) == "stat" && err == nil {
			statReads++
			if statReads >= 2 {
				return []byte(strings.Replace(string(data), "98765", "98766", 1)), nil
			}
		}
		return data, err
	}
	_, _, err := fs.ReadProcess(42)
	var gone *ProcessGoneError
	if !errors.As(err, &gone) {
		t.Fatalf("error = %v; want ProcessGoneError", err)
	}
}

func TestProcFSDetectsDisappearance(t *testing.T) {
	fs := NewProcFS("testdata/proc")
	statReads := 0
	fs.readFile = func(path string) ([]byte, error) {
		if filepath.Base(path) == "stat" {
			statReads++
			if statReads == 2 {
				return nil, os.ErrNotExist
			}
		}
		return os.ReadFile(path)
	}
	_, _, err := fs.ReadProcess(42)
	var gone *ProcessGoneError
	if !errors.As(err, &gone) {
		t.Fatalf("error = %v; want ProcessGoneError", err)
	}
}

func copyFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dst := filepath.Join(root, "42")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"stat", "status", "cmdline", "smaps_rollup"} {
		data, err := os.ReadFile(filepath.Join("testdata/proc/42", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
