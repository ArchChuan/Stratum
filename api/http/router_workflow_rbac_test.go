package http

import (
	"os"
	"strings"
	"testing"
)

func TestWorkflowRunRoutesSplitMemberOwnershipAndAdminControl(t *testing.T) {
	data, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	if !strings.Contains(source, `runs := r.Group("/workflow-runs", member...)`) {
		t.Fatal("workflow run reads and own cancel must be available to tenant members")
	}
	if !strings.Contains(source, `startRuns := r.Group("/workflow-runs", member...)`) || !strings.Contains(source, `startRuns.POST("", requireActive, h.StartRun)`) {
		t.Fatal("workflow start must remain available to active tenant members")
	}
	// 发起人可暂停/继续/取消自己发起的运行：router 只保 requireActive，
	// 归属校验在 application 层 authorizeRun 完成（非发起人 → ErrNotFound）。
	for _, route := range []string{
		`runs.GET("", h.ListRuns)`,
		`runs.GET("/:id", h.GetRun)`,
		`runs.GET("/:id/events", h.GetEvents)`,
		`runs.POST("/:id/cancel", requireActive, h.CancelRun)`,
		`runs.POST("/:id/pause", requireActive, h.PauseRun)`,
		`runs.POST("/:id/resume", requireActive, h.ResumeRun)`,
	} {
		if !strings.Contains(source, route) {
			t.Fatalf("workflow route has wrong role boundary: %s", route)
		}
	}
}
