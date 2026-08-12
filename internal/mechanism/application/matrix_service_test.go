package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/mechanism/domain"
	"github.com/byteBuilderX/stratum/internal/mechanism/domain/port"
)

// fakeEvaluator 是 MatrixEvaluator 的内存替身（测试只 mock 外部依赖）。
type fakeEvaluator struct {
	suites         []port.BenchmarkSuite
	runs           []port.MatrixRun
	ensureRevID    string
	ensureErr      error
	startErr       error
	listErr        error
	latestErr      error
	started        []string // 触发的 familyKey 序列（断言用）
	gotRequestedBy string
	ensureCalls    int
}

func (f *fakeEvaluator) ListBenchmarkSuites(_ context.Context, _ string) ([]port.BenchmarkSuite, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.suites, nil
}

func (f *fakeEvaluator) EnsureBenchmarkSuite(_ context.Context, _ string) (string, error) {
	f.ensureCalls++
	if f.ensureErr != nil {
		return "", f.ensureErr
	}
	return f.ensureRevID, nil
}

func (f *fakeEvaluator) StartMatrixRun(_ context.Context, _ string, familyKey, _, requestedBy string) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.started = append(f.started, familyKey)
	f.gotRequestedBy = requestedBy
	return nil
}

func (f *fakeEvaluator) LatestMatrixRuns(_ context.Context, _ string, _ []string) ([]port.MatrixRun, error) {
	if f.latestErr != nil {
		return nil, f.latestErr
	}
	return f.runs, nil
}

func newMatrixService(eval *fakeEvaluator, profiles []domain.Profile) *MatrixService {
	repo := &fakeRepo{profiles: profiles}
	return NewMatrixService(NewService(repo), eval)
}

func matrixRun(family string, passRate, cost, latency float64) port.MatrixRun {
	return port.MatrixRun{
		FamilyKey:  family,
		RunID:      "run-" + family,
		Passed:     passRate >= 1,
		PassRate:   passRate,
		TotalCost:  cost,
		AvgLatency: latency,
		TotalCases: 10,
		Status:     "succeeded",
	}
}

func TestRunMatrixTriggersAllProfilesAndSeedsSuite(t *testing.T) {
	eval := &fakeEvaluator{ensureRevID: "rev-1"}
	svc := newMatrixService(eval, []domain.Profile{
		profile("qwen", "qwen", domain.ProfileStatusActive),
		profile("deepseek", "deepseek", domain.ProfileStatusDraft),
	})

	got, err := svc.RunMatrix(context.Background(), "t1", "admin-1")
	if err != nil {
		t.Fatalf("RunMatrix: %v", err)
	}
	if got.SuiteRevisionID != "rev-1" || got.TriggeredCount != 2 {
		t.Fatalf("unexpected result: %+v", got)
	}
	if len(eval.started) != 2 || eval.started[0] != "qwen" || eval.started[1] != "deepseek" {
		t.Fatalf("expected both profiles queued in order, got %v", eval.started)
	}
	if eval.gotRequestedBy != "admin-1" {
		t.Fatalf("expected requestedBy forwarded, got %q", eval.gotRequestedBy)
	}
}

func TestRunMatrixWithoutProfilesReturnsErrorAndSkipsSeed(t *testing.T) {
	eval := &fakeEvaluator{ensureRevID: "rev-1"}
	svc := newMatrixService(eval, nil)

	_, err := svc.RunMatrix(context.Background(), "t1", "")
	if !errors.Is(err, ErrMatrixNoProfiles) {
		t.Fatalf("expected ErrMatrixNoProfiles, got %v", err)
	}
	if eval.ensureCalls != 0 {
		t.Fatal("benchmark suite must not be seeded when no profiles exist")
	}
}

func TestRunMatrixPropagatesEvaluatorErrors(t *testing.T) {
	t.Run("ensure failure propagates", func(t *testing.T) {
		eval := &fakeEvaluator{ensureErr: errors.New("suite down")}
		svc := newMatrixService(eval, []domain.Profile{profile("qwen", "qwen", "")})
		if _, err := svc.RunMatrix(context.Background(), "t1", ""); err == nil {
			t.Fatal("expected ensure error to propagate (fail closed)")
		}
	})

	t.Run("start failure propagates", func(t *testing.T) {
		eval := &fakeEvaluator{startErr: errors.New("queue down")}
		svc := newMatrixService(eval, []domain.Profile{profile("qwen", "qwen", "")})
		if _, err := svc.RunMatrix(context.Background(), "t1", ""); err == nil {
			t.Fatal("expected start error to propagate (fail closed)")
		}
	})
}

func TestGetMatrixFillsCellsFromLatestRuns(t *testing.T) {
	eval := &fakeEvaluator{
		suites: []port.BenchmarkSuite{{ID: "suite-1", Name: "机制基准集", CaseCount: 10}},
		runs:   []port.MatrixRun{matrixRun("qwen", 0.9, 1.2, 300)},
	}
	svc := newMatrixService(eval, []domain.Profile{
		profile("qwen", "qwen", domain.ProfileStatusActive),
		profile("deepseek", "deepseek", domain.ProfileStatusDraft),
	})

	report, err := svc.GetMatrix(context.Background(), "t1")
	if err != nil {
		t.Fatalf("GetMatrix: %v", err)
	}
	if len(report.Suites) != 1 {
		t.Fatalf("expected 1 suite, got %d", len(report.Suites))
	}
	if len(report.Cells) != 2 {
		t.Fatalf("expected 2 cells, got %d", len(report.Cells))
	}
	qwen := report.Cells[0]
	if qwen.RunID != "run-qwen" || qwen.PassRate != 0.9 || qwen.TotalCost != 1.2 || qwen.AvgLatency != 300 {
		t.Fatalf("expected run metrics in qwen cell, got %+v", qwen)
	}
	// 无 run 的档案不参与前沿
	if len(report.FrontierKeys) != 1 || report.FrontierKeys[0] != "qwen" {
		t.Fatalf("expected only qwen on frontier, got %v", report.FrontierKeys)
	}
}

func TestGetMatrixEmptyReportWhenNoRuns(t *testing.T) {
	eval := &fakeEvaluator{suites: []port.BenchmarkSuite{}}
	svc := newMatrixService(eval, []domain.Profile{profile("qwen", "qwen", "")})

	report, err := svc.GetMatrix(context.Background(), "t1")
	if err != nil {
		t.Fatalf("GetMatrix: %v", err)
	}
	if len(report.Cells) != 1 || report.Cells[0].RunID != "" {
		t.Fatalf("expected empty cell, got %+v", report.Cells)
	}
	if len(report.FrontierKeys) != 0 {
		t.Fatalf("expected empty frontier, got %v", report.FrontierKeys)
	}
}

func TestGetMatrixPropagatesEvaluatorErrors(t *testing.T) {
	t.Run("list suites failure", func(t *testing.T) {
		eval := &fakeEvaluator{listErr: errors.New("db down")}
		svc := newMatrixService(eval, []domain.Profile{profile("qwen", "qwen", "")})
		if _, err := svc.GetMatrix(context.Background(), "t1"); err == nil {
			t.Fatal("expected list error to propagate (fail closed)")
		}
	})

	t.Run("latest runs failure", func(t *testing.T) {
		eval := &fakeEvaluator{latestErr: errors.New("db down")}
		svc := newMatrixService(eval, []domain.Profile{profile("qwen", "qwen", "")})
		if _, err := svc.GetMatrix(context.Background(), "t1"); err == nil {
			t.Fatal("expected latest error to propagate (fail closed)")
		}
	})
}

func TestMarkFrontierPicksParetoNonDominated(t *testing.T) {
	cases := []struct {
		name  string
		cells []MatrixCell
		want  []string
	}{
		{
			name: "single data point is frontier",
			cells: []MatrixCell{
				{FamilyKey: "a", RunID: "r1", PassRate: 0.8, TotalCost: 1, AvgLatency: 100},
			},
			want: []string{"a"},
		},
		{
			name: "dominated cell dropped",
			cells: []MatrixCell{
				{FamilyKey: "good", RunID: "r1", PassRate: 0.9, TotalCost: 1, AvgLatency: 100},
				{FamilyKey: "bad", RunID: "r2", PassRate: 0.5, TotalCost: 2, AvgLatency: 300},
			},
			want: []string{"good"},
		},
		{
			name: "trade-off keeps both",
			cells: []MatrixCell{
				{FamilyKey: "fast", RunID: "r1", PassRate: 0.6, TotalCost: 1, AvgLatency: 100},
				{FamilyKey: "accurate", RunID: "r2", PassRate: 0.95, TotalCost: 2, AvgLatency: 500},
			},
			want: []string{"fast", "accurate"},
		},
		{
			name: "cells without runs are excluded",
			cells: []MatrixCell{
				{FamilyKey: "measured", RunID: "r1", PassRate: 0.8, TotalCost: 1, AvgLatency: 100},
				{FamilyKey: "never-run"},
			},
			want: []string{"measured"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := annotateFrontier(tc.cells)
			if len(got) != len(tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("expected %v, got %v", tc.want, got)
				}
			}
		})
	}
}

func TestAdoptProfilePromotesDraftToActive(t *testing.T) {
	draft := profile("qwen", "qwen", domain.ProfileStatusDraft)
	draft.Version = 1 // fakeRepo 直注入，模拟 DB 中已落库的档案（version 从 1 起）
	repo := &fakeRepo{profiles: []domain.Profile{draft}}
	svc := NewMatrixService(NewService(repo), &fakeEvaluator{})

	if err := svc.AdoptProfile(context.Background(), "qwen", "ops"); err != nil {
		t.Fatalf("AdoptProfile: %v", err)
	}
	got, err := svc.profiles.GetByFamilyKey(context.Background(), "qwen")
	if err != nil {
		t.Fatalf("GetByFamilyKey: %v", err)
	}
	if got.Status != domain.ProfileStatusActive {
		t.Fatalf("expected active status, got %q", got.Status)
	}
	if got.Version != 2 {
		t.Fatalf("expected version bump to 2, got %d", got.Version)
	}
}

func TestAdoptProfileRejectsNonDraft(t *testing.T) {
	svc := newMatrixService(&fakeEvaluator{}, []domain.Profile{
		profile("qwen", "qwen", domain.ProfileStatusActive),
	})
	err := svc.AdoptProfile(context.Background(), "qwen", "ops")
	if !errors.Is(err, ErrAdoptInvalidTransition) {
		t.Fatalf("expected ErrAdoptInvalidTransition, got %v", err)
	}
}

func TestAdoptProfileUnknownFamilyKey(t *testing.T) {
	svc := newMatrixService(&fakeEvaluator{}, nil)
	if err := svc.AdoptProfile(context.Background(), "ghost", "ops"); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("expected ErrProfileNotFound, got %v", err)
	}
}
