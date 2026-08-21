package application

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// getStore 让 RevisionService.Get 可返回 payload / 失败。
type getStore struct {
	payload []byte
	err     error
}

func (g *getStore) Put(context.Context, port.RevisionPayload) (port.RevisionPayloadRef, error) {
	return port.RevisionPayloadRef{}, nil
}
func (g *getStore) Get(context.Context, port.RevisionPayloadRef) ([]byte, error) {
	return g.payload, g.err
}
func (g *getStore) Delete(context.Context, port.RevisionPayloadRef) error { return nil }

func validRevisionRef() domain.ResourceRef {
	return domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "rev-1"}
}

func TestRevisionServiceGet(t *testing.T) {
	repo := &fakeRevisionRepository{
		getResult: domain.ResourceRevision{ID: "r1", PayloadRef: "object://x", PayloadHash: "h"},
		getFound:  true,
	}
	store := &getStore{payload: []byte("encrypted-payload")}
	svc := NewRevisionService(store, repo)

	rev, payload, found, err := svc.Get(context.Background(), "t1", validRevisionRef())
	if err != nil || !found || rev.ID != "r1" || string(payload) != "encrypted-payload" {
		t.Fatalf("get = %+v, %q, %v, %v", rev, payload, found, err)
	}
}

func TestRevisionServiceGetEdgeCases(t *testing.T) {
	repo := &fakeRevisionRepository{getResult: domain.ResourceRevision{ID: "r1"}}
	store := &getStore{payload: []byte("p")}
	svc := NewRevisionService(store, repo)

	// 极端情况：依赖缺失。
	nilSvc := NewRevisionService(nil, nil)
	if _, _, _, err := nilSvc.Get(context.Background(), "t1", validRevisionRef()); err == nil {
		t.Fatal("nil deps must error")
	}
	// 极端情况：空 tenant。
	if _, _, _, err := svc.Get(context.Background(), " ", validRevisionRef()); err == nil {
		t.Fatal("empty tenant must error")
	}
	// 极端情况：非法 ref。
	if _, _, _, err := svc.Get(context.Background(), "t1", domain.ResourceRef{}); err == nil {
		t.Fatal("invalid ref must error")
	}
	// 极端情况：repo 未找到。
	repoNotFound := &fakeRevisionRepository{getFound: false}
	if _, _, found, err := NewRevisionService(store, repoNotFound).Get(context.Background(), "t1", validRevisionRef()); err != nil || found {
		t.Fatalf("not found = %v, %v", found, err)
	}
	// 极端情况：payload 加载失败。
	repoFound := &fakeRevisionRepository{getResult: domain.ResourceRevision{ID: "r1", PayloadRef: "object://x"}, getFound: true}
	storeErr := &getStore{err: errors.New("object down")}
	if _, _, _, err := NewRevisionService(storeErr, repoFound).Get(context.Background(), "t1", validRevisionRef()); err == nil {
		t.Fatal("store failure must error")
	}
}

func TestSuiteServiceGetRevision(t *testing.T) {
	repo := &fakeSuiteRepo{revision: domain.EvalSuiteRevision{ID: "r1", SuiteID: "s1"}}
	svc := NewSuiteService(repo)

	rev, err := svc.GetRevision(context.Background(), "t1", "r1")
	if err != nil || rev.ID != "r1" {
		t.Fatalf("get revision = %+v, %v", rev, err)
	}
	// 极端情况：未找到 → ErrSuiteNotFound。
	if _, err := svc.GetRevision(context.Background(), "t1", "ghost"); !errors.Is(err, ErrSuiteNotFound) {
		t.Fatalf("ghost err = %v", err)
	}
}

func TestJobServiceGet(t *testing.T) {
	repo := &fakeJobRepo{enqueued: domain.EvaluationJob{ID: "j1"}}
	svc := NewJobService(repo, nil)

	job, err := svc.Get(context.Background(), "t1", "j1")
	if err != nil || job.ID != "j1" {
		t.Fatalf("get = %+v, %v", job, err)
	}
	// 极端情况：未找到 → ErrJobNotFound。
	if _, err := svc.Get(context.Background(), "t1", "ghost"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("ghost err = %v", err)
	}
}

func TestQueryServiceOverview(t *testing.T) {
	svc := NewQueryService(&queryRepoStub{})
	if _, err := svc.Overview(context.Background(), "t1"); err != nil {
		t.Fatalf("overview = %v", err)
	}
	// 极端情况：repo 错误传播。
	svc = NewQueryService(&queryRepoStub{err: errors.New("db down")})
	if _, err := svc.Overview(context.Background(), "t1"); err == nil {
		t.Fatal("repo error must propagate")
	}
}

func TestWorkerStartStop(t *testing.T) {
	// runner 线程安全：worker goroutine 与测试并发读写。
	runner := &safeJobRunner{}
	worker := NewWorker(fakeTenantLister{ids: []string{"tenant-a"}}, runner, time.Millisecond, observability.NoopMetrics{})

	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)
	// 等 ticker 至少触发一次。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runner.count() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if runner.count() == 0 {
		t.Fatal("ticker must trigger PollOnce")
	}
	// 极端情况：Stop 等待 goroutine 退出且幂等。
	worker.Stop()
	worker.Stop()
	cancel()
}

// safeJobRunner 带锁计数，避免 worker goroutine 与测试的 -race 竞争。
type safeJobRunner struct {
	mu    sync.Mutex
	calls int
}

func (r *safeJobRunner) RunOnce(context.Context, string, string, time.Duration) (bool, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return true, nil
}

func (r *safeJobRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func TestWorkerPollOnceErrors(t *testing.T) {
	// 极端情况：lister 失败 → 返回错误。
	worker := NewWorker(fakeTenantListerErr{}, &fakeTenantJobRunner{}, time.Second, observability.NoopMetrics{})
	if _, err := worker.PollOnce(context.Background()); err == nil {
		t.Fatal("lister failure must error")
	}
	// 极端情况：runner 失败 → 聚合错误，其余 tenant 继续。
	worker = NewWorker(fakeTenantLister{ids: []string{"a", "b"}}, &fakeRunnerErr{}, time.Second, observability.NoopMetrics{})
	if _, err := worker.PollOnce(context.Background()); err == nil {
		t.Fatal("runner failure must error")
	}
}

type fakeTenantListerErr struct{}

func (f fakeTenantListerErr) ListTenantIDs(context.Context) ([]string, error) {
	return nil, errors.New("db down")
}

type fakeRunnerErr struct{}

func (f *fakeRunnerErr) RunOnce(context.Context, string, string, time.Duration) (bool, error) {
	return false, errors.New("run boom")
}
