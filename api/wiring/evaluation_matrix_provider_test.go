package wiring

import (
	"context"
	"errors"
	"testing"

	evalapp "github.com/byteBuilderX/stratum/internal/evaluation/application"
	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	evalport "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	mechanismdomain "github.com/byteBuilderX/stratum/internal/mechanism/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// stubMatrixSuites 是 matrixSuiteService 的内存替身。
type stubMatrixSuites struct {
	active    evaldomain.EvalSuiteRevision
	activeErr error
	created   []evalapp.CreateSuiteInput
	published []string
}

func (s *stubMatrixSuites) GetActiveRevision(_ context.Context, _, _ string) (evaldomain.EvalSuiteRevision, error) {
	if s.activeErr != nil {
		return evaldomain.EvalSuiteRevision{}, s.activeErr
	}
	return s.active, nil
}

func (s *stubMatrixSuites) Create(_ context.Context, _ string, input evalapp.CreateSuiteInput) (evaldomain.EvalSuite, evaldomain.EvalSuiteRevision, error) {
	s.created = append(s.created, input)
	return evaldomain.EvalSuite{ID: "suite-new"}, evaldomain.EvalSuiteRevision{}, nil
}

func (s *stubMatrixSuites) Publish(_ context.Context, _ string, suiteID string) (evaldomain.EvalSuiteRevision, error) {
	s.published = append(s.published, suiteID)
	return evaldomain.EvalSuiteRevision{ID: "rev-" + suiteID, VersionNo: 1}, nil
}

// stubMatrixJobs 是 matrixJobService 的内存替身。
type stubMatrixJobs struct {
	inputs []evalapp.EnqueueRunInput
	err    error
}

func (s *stubMatrixJobs) EnqueueRun(_ context.Context, _ string, input evalapp.EnqueueRunInput) (evaldomain.EvaluationJob, error) {
	s.inputs = append(s.inputs, input)
	if s.err != nil {
		return evaldomain.EvaluationJob{}, s.err
	}
	return evaldomain.EvaluationJob{}, nil
}

// stubMatrixQuery 是 CenterQueryRepository 的内存替身，仅 ListSuites/ListRuns
// 参与矩阵流程。ListRuns 按 filter.ResourceID 过滤，模拟真实仓库的族键过滤。
type stubMatrixQuery struct {
	suites evaldomain.SuitePage
	runs   map[string]evaldomain.RunPage // key: familyKey
}

func (s *stubMatrixQuery) ListSuites(_ context.Context, _ string, _ evalport.CenterFilter) (evaldomain.SuitePage, error) {
	return s.suites, nil
}

func (s *stubMatrixQuery) ListRuns(_ context.Context, _ string, filter evalport.CenterFilter) (evaldomain.RunPage, error) {
	if page, ok := s.runs[filter.ResourceID]; ok {
		return page, nil
	}
	return evaldomain.RunPage{}, nil
}

func (s *stubMatrixQuery) Overview(context.Context, string) (evaldomain.CenterOverview, error) {
	panic("unused by matrix provider")
}

func (s *stubMatrixQuery) ListResources(context.Context, string, evalport.CenterFilter) (evaldomain.ResourcePage, error) {
	panic("unused by matrix provider")
}

func (s *stubMatrixQuery) ListCandidates(context.Context, string, evalport.CenterFilter) (evaldomain.CandidatePage, error) {
	panic("unused by matrix provider")
}

func (s *stubMatrixQuery) ListExperiments(context.Context, string, evalport.CenterFilter) (evaldomain.ExperimentPage, error) {
	panic("unused by matrix provider")
}

func (s *stubMatrixQuery) Timeline(context.Context, string, evalport.CenterFilter) (evaldomain.TimelinePage, error) {
	panic("unused by matrix provider")
}

// stubMatrixRuns 是 RunRepository 的内存替身，仅 GetRun 参与矩阵流程。
type stubMatrixRuns struct {
	run evaldomain.EvalRun
	ok  bool
}

func (s *stubMatrixRuns) GetRun(_ context.Context, _ string, _ string) (evaldomain.EvalRun, bool, error) {
	return s.run, s.ok, nil
}

func (s *stubMatrixRuns) SaveRun(context.Context, string, evaldomain.EvalRun) error {
	panic("unused by matrix provider")
}

func TestMatrixProviderEnsureBenchmarkSuite(t *testing.T) {
	cases := []struct {
		name      string
		suites    evaldomain.SuitePage
		active    evaldomain.EvalSuiteRevision
		activeOK  bool // 是否存在已发布 revision
		want      string
		wantSeeds int
	}{
		{
			name: "seeds benchmark suite when absent",
			suites: evaldomain.SuitePage{Items: []evaldomain.SuiteSummary{
				{ID: "s-other", Name: "其他套件"},
			}},
			want: "rev-suite-new", wantSeeds: 1,
		},
		{
			name: "reuses published benchmark revision",
			suites: evaldomain.SuitePage{Items: []evaldomain.SuiteSummary{
				{ID: "s-bench", Name: constants.MatrixBenchmarkSuiteName},
			}},
			active:   evaldomain.EvalSuiteRevision{ID: "rev-existing", VersionNo: 2},
			activeOK: true,
			want:     "rev-existing", wantSeeds: 0,
		},
		{
			name: "reseeds unpublished benchmark suite",
			suites: evaldomain.SuitePage{Items: []evaldomain.SuiteSummary{
				{ID: "s-bench", Name: constants.MatrixBenchmarkSuiteName},
			}},
			want: "rev-suite-new", wantSeeds: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			suites := &stubMatrixSuites{}
			if !tc.activeOK {
				suites.activeErr = evalapp.ErrSuiteNotFound
			} else {
				suites.active = tc.active
			}
			provider := &matrixEvaluatorProvider{
				suites: suites, jobs: &stubMatrixJobs{},
				query: &stubMatrixQuery{suites: tc.suites}, run: &stubMatrixRuns{},
				profiles: &stubProfileReader{profile: matrixProfile("fp-1", "qwen-max")},
			}
			got, err := provider.EnsureBenchmarkSuite(context.Background(), "t1")
			if err != nil {
				t.Fatalf("EnsureBenchmarkSuite: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
			if len(suites.created) != tc.wantSeeds {
				t.Fatalf("expected %d seeds, got %d", tc.wantSeeds, len(suites.created))
			}
			if len(suites.created) > 0 {
				seeded := suites.created[0]
				if seeded.Name != constants.MatrixBenchmarkSuiteName ||
					seeded.ResourceKind != evaldomain.ResourceKindMechanism || len(seeded.Cases) != 3 {
					t.Fatalf("unexpected seed input: %+v", seeded)
				}
			}
		})
	}
}

func TestMatrixProviderStartMatrixRun(t *testing.T) {
	jobs := &stubMatrixJobs{}
	provider := &matrixEvaluatorProvider{
		suites: &stubMatrixSuites{}, jobs: jobs,
		query: &stubMatrixQuery{}, run: &stubMatrixRuns{},
		profiles: &stubProfileReader{profile: matrixProfile("fp-1", "qwen-max")},
	}
	// 显式触发者身份被记录；空值回退到服务账号（见 TestMatrixProviderStartMatrixRunDefaultsIdentity）。
	err := provider.StartMatrixRun(context.Background(), "t1", "qwen", "suite-rev-1", "admin-1")
	if err != nil {
		t.Fatalf("StartMatrixRun: %v", err)
	}
	if len(jobs.inputs) != 1 {
		t.Fatalf("expected 1 enqueue, got %d", len(jobs.inputs))
	}
	input := jobs.inputs[0]
	if input.Resource.Kind != evaldomain.ResourceKindMechanism || input.Resource.ResourceID != "qwen" ||
		input.Resource.RevisionID != "fp-1" || input.SuiteRevisionID != "suite-rev-1" {
		t.Fatalf("unexpected enqueue input: %+v", input)
	}
	if input.IdempotencyKey != "matrix:qwen:fp-1:suite-rev-1" || input.RequestedBy != "admin-1" {
		t.Fatalf("unexpected idempotency/identity: %+v", input)
	}
}

func TestMatrixProviderStartMatrixRunDefaultsIdentity(t *testing.T) {
	jobs := &stubMatrixJobs{}
	provider := &matrixEvaluatorProvider{
		suites: &stubMatrixSuites{}, jobs: jobs,
		query: &stubMatrixQuery{}, run: &stubMatrixRuns{},
		profiles: &stubProfileReader{profile: matrixProfile("fp-1", "qwen-max")},
	}
	if err := provider.StartMatrixRun(context.Background(), "t1", "qwen", "suite-rev-1", ""); err != nil {
		t.Fatalf("StartMatrixRun: %v", err)
	}
	if len(jobs.inputs) != 1 || jobs.inputs[0].RequestedBy != matrixRunRequestedBy {
		t.Fatalf("expected service-account identity, got %+v", jobs.inputs)
	}
}

func TestMatrixProviderStartMatrixRunFailsClosed(t *testing.T) {
	cases := []struct {
		name    string
		profile mechanismdomain.Profile
		missing bool
	}{
		{name: "empty fingerprint refuses enqueue", profile: matrixProfile("", "qwen-max")},
		{name: "missing profile", profile: mechanismdomain.Profile{}, missing: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jobs := &stubMatrixJobs{}
			provider := &matrixEvaluatorProvider{
				suites: &stubMatrixSuites{}, jobs: jobs,
				query: &stubMatrixQuery{}, run: &stubMatrixRuns{},
				profiles: &stubProfileReader{profile: tc.profile, missing: tc.missing},
			}
			err := provider.StartMatrixRun(context.Background(), "t1", "qwen", "suite-rev-1", "")
			if err == nil {
				t.Fatal("expected fail-closed error, got nil")
			}
			if len(jobs.inputs) != 0 {
				t.Fatal("must not enqueue on fail-closed path")
			}
		})
	}
}

func TestMatrixProviderLatestMatrixRunsMapsMetrics(t *testing.T) {
	provider := &matrixEvaluatorProvider{
		suites: &stubMatrixSuites{}, jobs: &stubMatrixJobs{},
		query: &stubMatrixQuery{runs: map[string]evaldomain.RunPage{"qwen": {Items: []evaldomain.RunSummary{
			{ID: "run-1", Status: "completed"},
		}}}},
		run: &stubMatrixRuns{ok: true, run: evaldomain.EvalRun{
			ID: "run-1", Passed: true, TotalCases: 3,
			Metrics: map[string]any{"pass_rate": 0.66, "total_cost_usd": 0.12, "avg_latency_ms": 800.0},
		}},
		profiles: &stubProfileReader{profile: matrixProfile("fp-1", "qwen-max")},
	}
	runs, err := provider.LatestMatrixRuns(context.Background(), "t1", []string{"qwen", "nofamily"})
	if err != nil {
		t.Fatalf("LatestMatrixRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].FamilyKey != "qwen" {
		t.Fatalf("expected only evaluated family, got %+v", runs)
	}
	run := runs[0]
	if run.RunID != "run-1" || !run.Passed || run.TotalCases != 3 || run.Status != "completed" {
		t.Fatalf("unexpected run: %+v", run)
	}
	if run.PassRate != 0.66 || run.TotalCost != 0.12 || run.AvgLatency != 800 {
		t.Fatalf("unexpected metrics: %+v", run)
	}
}

func TestMatrixProviderLatestMatrixRunsMissingMetricsDefaultZero(t *testing.T) {
	provider := &matrixEvaluatorProvider{
		suites: &stubMatrixSuites{}, jobs: &stubMatrixJobs{},
		query: &stubMatrixQuery{runs: map[string]evaldomain.RunPage{"qwen": {Items: []evaldomain.RunSummary{
			{ID: "run-1", Status: "running"},
		}}}},
		run:      &stubMatrixRuns{ok: true, run: evaldomain.EvalRun{ID: "run-1", Metrics: map[string]any{}}},
		profiles: &stubProfileReader{profile: matrixProfile("fp-1", "qwen-max")},
	}
	runs, err := provider.LatestMatrixRuns(context.Background(), "t1", []string{"qwen"})
	if err != nil {
		t.Fatalf("LatestMatrixRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].PassRate != 0 || runs[0].TotalCost != 0 || runs[0].AvgLatency != 0 {
		t.Fatalf("missing metrics must default to zero, got %+v", runs)
	}
}

func TestMatrixProviderListBenchmarkSuites(t *testing.T) {
	suites := evaldomain.SuitePage{Items: []evaldomain.SuiteSummary{
		{ID: "s1", Name: "基准集 A", Description: "d1"},
		{ID: "s2", Name: "基准集 B", Description: "d2"},
	}}
	cases := []struct {
		name      string
		active    evaldomain.EvalSuiteRevision
		activeErr error
		wantRev   string
		wantCases int
	}{
		{name: "fills active revision and case count", active: evaldomain.EvalSuiteRevision{VersionNo: 3, Cases: []evaldomain.EvalCase{{}, {}}}, wantRev: "v3", wantCases: 2},
		{name: "skips suite without published revision", activeErr: evalapp.ErrSuiteNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &matrixEvaluatorProvider{
				suites: &stubMatrixSuites{active: tc.active, activeErr: tc.activeErr},
				jobs:   &stubMatrixJobs{}, query: &stubMatrixQuery{suites: suites},
				run: &stubMatrixRuns{}, profiles: &stubProfileReader{profile: matrixProfile("fp-1", "qwen-max")},
			}
			got, err := provider.ListBenchmarkSuites(context.Background(), "t1")
			if err != nil {
				t.Fatalf("ListBenchmarkSuites: %v", err)
			}
			if len(got) != 2 {
				t.Fatalf("expected 2 suites, got %d", len(got))
			}
			for _, suite := range got {
				if suite.ActiveRevision != tc.wantRev || suite.CaseCount != tc.wantCases {
					t.Fatalf("unexpected suite fill: %+v", suite)
				}
			}
		})
	}
}

func TestMatrixProviderListBenchmarkSuitesPropagatesFailure(t *testing.T) {
	provider := &matrixEvaluatorProvider{
		suites: &stubMatrixSuites{activeErr: errors.New("boom")},
		jobs:   &stubMatrixJobs{},
		query:  &stubMatrixQuery{suites: evaldomain.SuitePage{Items: []evaldomain.SuiteSummary{{ID: "s1", Name: "基准集 A"}}}},
		run:    &stubMatrixRuns{}, profiles: &stubProfileReader{profile: matrixProfile("fp-1", "qwen-max")},
	}
	if _, err := provider.ListBenchmarkSuites(context.Background(), "t1"); err == nil {
		t.Fatal("expected upstream failure propagation")
	}
}

func TestMatrixProviderBenchmarkCasesShape(t *testing.T) {
	cases := benchmarkMatrixCases()
	if len(cases) != 3 {
		t.Fatalf("expected 3 benchmark cases, got %d", len(cases))
	}
	seen := map[string]bool{}
	for _, tc := range cases {
		if tc.AssertionMode != evaldomain.AssertionJudge || !tc.Enabled || tc.JudgeSpec == nil || tc.JudgeSpec.Model != "" {
			t.Fatalf("benchmark case must be judge-enabled with platform-default model: %+v", tc)
		}
		key, ok := tc.Input.(map[string]string)["template"]
		if !ok || seen[key] {
			t.Fatalf("benchmark case template missing or duplicated: %+v", tc)
		}
		seen[key] = true
		switch key {
		case "memory_extraction", "memory_summary", "compaction":
		default:
			t.Fatalf("benchmark case template %q not among six baseline keys", key)
		}
	}
}
