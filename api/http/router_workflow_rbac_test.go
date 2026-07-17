package http

import (
	"os"
	"strings"
	"testing"
)

func TestWorkflowSensitiveRunRoutesRequireAdmin(t *testing.T) {
	data, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	if !strings.Contains(source, `runs := r.Group("/workflow-runs", append(member, admin)...)`) {
		t.Fatal("workflow run read/control routes must require tenant admin")
	}
	if !strings.Contains(source, `startRuns := r.Group("/workflow-runs", member...)`) || !strings.Contains(source, `startRuns.POST("", requireActive, h.StartRun)`) {
		t.Fatal("workflow start must remain available to active tenant members")
	}
}
