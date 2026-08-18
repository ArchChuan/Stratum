package application

import (
	"strings"
	"testing"
)

func TestBuildNoAnswer(t *testing.T) {
	cases := []struct {
		name                string
		reason              NoAnswerReason
		retrieved, filtered int
		bestScore           float32
		detailHas           []string // Detail 必须包含的子串
	}{
		{name: "no sources", reason: NoAnswerNoSources, detailHas: []string{"未检索到"}},
		{name: "threshold filtered", reason: NoAnswerThresholdFiltered, retrieved: 12, filtered: 10, bestScore: 0.42, detailHas: []string{"12 条候选", "10 条未达", "0.420"}},
		{name: "access restricted", reason: NoAnswerAccessRestricted, detailHas: []string{"无可见文档"}},
		{name: "insufficient evidence", reason: NoAnswerInsufficientEvidence, detailHas: []string{"证据不足"}},
		{name: "unsupported mode", reason: NoAnswerUnsupportedMode, detailHas: []string{"不被支持"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := buildNoAnswer(tc.reason, tc.retrieved, tc.filtered, tc.bestScore)
			if info.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", info.Reason, tc.reason)
			}
			if info.RetrievedCount != tc.retrieved || info.FilteredCount != tc.filtered {
				t.Fatalf("counts = (%d,%d), want (%d,%d)", info.RetrievedCount, info.FilteredCount, tc.retrieved, tc.filtered)
			}
			if info.BestScore != tc.bestScore {
				t.Fatalf("bestScore = %f, want %f", info.BestScore, tc.bestScore)
			}
			if info.Retried || info.RewrittenQuery != "" {
				t.Fatalf("retry fields must default zero, got retried=%v query=%q", info.Retried, info.RewrittenQuery)
			}
			for _, want := range tc.detailHas {
				if !strings.Contains(info.Detail, want) {
					t.Fatalf("detail %q missing %q", info.Detail, want)
				}
			}
		})
	}
}

func TestNoAnswerSeverity(t *testing.T) {
	// access_restricted（权限语义）必须高于质量门控类；threshold_filtered 与
	// insufficient_evidence 同级；no_sources/unsupported_mode 最低。
	if s := noAnswerSeverity(NoAnswerAccessRestricted); s <= noAnswerSeverity(NoAnswerThresholdFiltered) {
		t.Fatalf("access_restricted severity %d must exceed threshold_filtered", s)
	}
	if s := noAnswerSeverity(NoAnswerThresholdFiltered); s != noAnswerSeverity(NoAnswerInsufficientEvidence) {
		t.Fatalf("threshold_filtered %d != insufficient_evidence %d", s, noAnswerSeverity(NoAnswerInsufficientEvidence))
	}
	if s := noAnswerSeverity(NoAnswerNoSources); s != noAnswerSeverity(NoAnswerUnsupportedMode) {
		t.Fatalf("no_sources %d != unsupported_mode %d", s, noAnswerSeverity(NoAnswerUnsupportedMode))
	}
	if s := noAnswerSeverity(NoAnswerNoSources); s >= noAnswerSeverity(NoAnswerThresholdFiltered) {
		t.Fatalf("no_sources severity %d must be below quality-gated reasons", s)
	}
}

func TestMergeNoAnswer(t *testing.T) {
	restricted := buildNoAnswer(NoAnswerAccessRestricted, 0, 0, 0)
	filtered := buildNoAnswer(NoAnswerThresholdFiltered, 10, 8, 0.31)
	empty := buildNoAnswer(NoAnswerNoSources, 0, 0, 0)

	cases := []struct {
		name     string
		acc, cur *NoAnswerInfo
		want     *NoAnswerInfo // nil = 有答案
	}{
		{name: "both nil", acc: nil, cur: nil, want: nil},
		{name: "nil then empty", acc: nil, cur: empty, want: empty},
		{name: "empty then nil", acc: empty, cur: nil, want: nil}, // 任一有答案即整体有答案
		{name: "restricted wins over filtered", acc: filtered, cur: restricted, want: restricted},
		{name: "filtered wins over empty", acc: empty, cur: filtered, want: filtered},
		{name: "equal severity keeps first", acc: empty, cur: empty, want: empty},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeNoAnswer(tc.acc, tc.cur)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("merge = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("merge = nil, want signal")
			}
			if got.Reason != tc.want.Reason {
				t.Fatalf("reason = %q, want %q", got.Reason, tc.want.Reason)
			}
		})
	}
}
