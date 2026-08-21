package application

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

func TestBuildTrajectorySkeleton_TruncatesAndAggregates(t *testing.T) {
	sk, err := BuildTrajectorySkeleton("exec-1", "完成调研", "找到了答案", "",
		[]ToolCallInput{
			{ToolName: "search", ArgsSummary: strings.Repeat("a", 500), Status: domain.TrajectoryStepStatusSuccess, DurationMS: 10},
			{ToolName: "search", ArgsSummary: "x", Status: domain.TrajectoryStepStatusError, ErrorMsg: strings.Repeat("e", 500), DurationMS: 20},
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(sk.Steps) != 2 {
		t.Fatalf("steps=%d, want 2", len(sk.Steps))
	}
	if got := len([]rune(sk.Steps[0].ArgsSummary)); got > constants.MemoryReflectionArgsSummaryMaxRunes {
		t.Fatalf("args summary not truncated: %d runes", got)
	}
	if got := len([]rune(sk.Steps[1].ErrorFingerprint)); got > constants.MemoryReflectionErrorFingerprintMaxRunes {
		t.Fatalf("error fingerprint not truncated: %d runes", got)
	}
	if sk.ToolStats["search"].Count != 2 || sk.ToolStats["search"].ErrorCount != 1 {
		t.Fatalf("unexpected stats: %#v", sk.ToolStats)
	}
}

func TestBuildTrajectorySkeleton_SizeBudgetDropsTailSteps(t *testing.T) {
	calls := make([]ToolCallInput, 0, 60)
	for i := 0; i < 60; i++ {
		calls = append(calls, ToolCallInput{
			ToolName:    "tool",
			ArgsSummary: strings.Repeat("参数", 100),
			Status:      domain.TrajectoryStepStatusSuccess,
		})
	}
	sk, err := BuildTrajectorySkeleton("exec-1", "", "", "", calls)
	if err != nil {
		t.Fatal(err)
	}
	if len(sk.Steps) > constants.MemoryReflectionStepMax {
		t.Fatalf("steps=%d exceed step max %d", len(sk.Steps), constants.MemoryReflectionStepMax)
	}
	raw, err := json.Marshal(sk)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > constants.MemoryReflectionSkeletonMaxBytes {
		t.Fatalf("skeleton size %d exceeds budget %d", len(raw), constants.MemoryReflectionSkeletonMaxBytes)
	}
}

func TestBuildTrajectorySkeleton_RejectsEmptyExecutionID(t *testing.T) {
	if _, err := BuildTrajectorySkeleton("", "", "", "", nil); err == nil {
		t.Fatal("empty execution id must fail")
	}
}
