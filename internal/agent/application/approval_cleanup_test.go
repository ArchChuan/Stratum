package application

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"go.uber.org/zap"
)

// mockApprovalCleanupRepo 只关心 ExpireStale 的调用审计，其余 ToolApprovalRepo
// 方法空实现以满足接口（worker 构造参数是 port 接口）。
type mockApprovalCleanupRepo struct {
	expireStale      int64
	expireStaleCalls int
	mu               sync.Mutex
}

func (m *mockApprovalCleanupRepo) ExpireStale(_ context.Context, _ string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireStaleCalls++
	return m.expireStale, nil
}
func (*mockApprovalCleanupRepo) Create(context.Context, string, domain.ToolApproval) (string, error) {
	return "", nil
}
func (*mockApprovalCleanupRepo) Get(context.Context, string, string) (domain.ToolApproval, error) {
	return domain.ToolApproval{}, nil
}
func (*mockApprovalCleanupRepo) Decide(context.Context, string, string, string, string, string, time.Time) error {
	return nil
}
func (*mockApprovalCleanupRepo) ClaimExecution(context.Context, string, string) error { return nil }
func (*mockApprovalCleanupRepo) ReleaseExecution(context.Context, string, string) error {
	return nil
}
func (*mockApprovalCleanupRepo) MarkOutcomeUnknown(context.Context, string, string) error {
	return nil
}
func (*mockApprovalCleanupRepo) MarkExecuted(context.Context, string, string) error { return nil }
func (*mockApprovalCleanupRepo) ListPending(context.Context, string, string) ([]domain.ToolApproval, error) {
	return nil, nil
}
func (*mockApprovalCleanupRepo) ListActionable(context.Context, string, string) ([]domain.ToolApproval, error) {
	return nil, nil
}
func (*mockApprovalCleanupRepo) ListHistory(context.Context, string, string, int, int) ([]domain.ToolApproval, int, error) {
	return nil, 0, nil
}
func (*mockApprovalCleanupRepo) Cancel(context.Context, string, string, string, string, time.Time) error {
	return nil
}
func (*mockApprovalCleanupRepo) Invalidate(context.Context, string, string, string) error {
	return nil
}
func (*mockApprovalCleanupRepo) Void(context.Context, string, string, string) error { return nil }
func (*mockApprovalCleanupRepo) InvalidateStaleForTool(context.Context, string, string, string, string) (int64, error) {
	return 0, nil
}
func (*mockApprovalCleanupRepo) UpdateAssignee(context.Context, string, string, string) error {
	return nil
}
func (*mockApprovalCleanupRepo) CascadeByConversation(context.Context, string, string) error {
	return nil
}

func TestApprovalCleanupWorkerExpiresStale(t *testing.T) {
	repo := &mockApprovalCleanupRepo{expireStale: 3}
	worker := NewApprovalCleanupWorker(
		func(context.Context) ([]string, error) { return []string{"tenant-1"}, nil },
		repo, 10*time.Millisecond, zap.NewNop(), observability.NoopMetrics{},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	worker.Start(ctx)
	select {
	case <-ctx.Done():
	case <-time.After(150 * time.Millisecond):
	}
	worker.Stop()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.expireStaleCalls == 0 {
		t.Fatal("ExpireStale should have been called")
	}
}
