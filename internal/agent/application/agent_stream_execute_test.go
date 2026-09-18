package application

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// fakeLeaseRepo 是租约端口的内存替身，用于验证三路分支的判定。
type fakeLeaseRepo struct {
	status    port.LeaseStatus
	claimGen  int
	claimErr  error
	stampGen  int
	stampErr  error
	claimCall int
}

func (f *fakeLeaseRepo) StampLease(context.Context, string, string, time.Duration) (int, error) {
	return f.stampGen, f.stampErr
}

func (f *fakeLeaseRepo) ClaimLease(context.Context, string, string, int, time.Duration) (int, error) {
	f.claimCall++
	if f.claimErr != nil {
		return 0, f.claimErr
	}
	return f.claimGen, nil
}

func (f *fakeLeaseRepo) RenewLease(context.Context, string, string, int, time.Duration) error {
	return nil
}

func (f *fakeLeaseRepo) ReleaseLease(context.Context, string, string, int) error { return nil }

func (f *fakeLeaseRepo) LeaseStatus(context.Context, string, string) (port.LeaseStatus, error) {
	return f.status, nil
}

// streamNoopCheckpointStore 是「租户限定查询查不到」的替身（GetLatest → (nil,nil)），
// 用于断言带 execution_id 却查不到时返回 404 语义。
//
// 必须在 package application 内声明：同名替身 noopCheckpointStore 只存在于
// agent_service_extra_test.go 的 application_test 包里，对包内测试不可见，
// 而本文件要读写 svc.deps（非导出字段），只能是 package application。
type streamNoopCheckpointStore struct{}

func (streamNoopCheckpointStore) Upsert(context.Context, string, domain.AgentExecutionCheckpoint) error {
	return nil
}
func (streamNoopCheckpointStore) GetLatest(context.Context, string, string) (*domain.AgentExecutionCheckpoint, error) {
	return nil, nil
}
func (streamNoopCheckpointStore) MarkCompleted(context.Context, string, string) error { return nil }
func (streamNoopCheckpointStore) UpdateStatus(context.Context, string, string, string) error {
	return nil
}
func (streamNoopCheckpointStore) DeleteExpired(context.Context, string) (int64, error) { return 0, nil }
func (streamNoopCheckpointStore) GetLatestActiveByConversation(context.Context, string, string) (*domain.AgentExecutionCheckpoint, error) {
	return nil, nil
}
func (streamNoopCheckpointStore) UpdateStatusFrom(context.Context, string, string, string, string) error {
	return nil
}
func (streamNoopCheckpointStore) AdvanceRunGeneration(context.Context, string, string, int) error {
	return nil
}
func (streamNoopCheckpointStore) Terminate(context.Context, string, string, string) error { return nil }

// streamResumableCheckpointStore 是「租户限定查询查得到」的替身，用于续跑路径。
// 嵌入 streamNoopCheckpointStore 只为继承其余 8 个方法——本用例只走 GetLatest。
type streamResumableCheckpointStore struct{ streamNoopCheckpointStore }

func (streamResumableCheckpointStore) GetLatest(context.Context, string, string) (*domain.AgentExecutionCheckpoint, error) {
	return &domain.AgentExecutionCheckpoint{}, nil
}

func TestOpenStreamSubscriptionTailsWhenLeaseActive(t *testing.T) {
	store := &fakeStreamStore{block: 5 * time.Millisecond}
	lease := &fakeLeaseRepo{status: port.LeaseStatus{Generation: 3, Active: true}}
	_, _ = store.Append(context.Background(), "e1", 3, port.StreamEntry{
		Event: port.StreamEventDone, Payload: `{"done":true}`})

	svc := NewAgentService(AgentServiceDeps{})
	svc.deps.StreamStore = store
	svc.deps.ControlBus = &fakeControlBus{}
	svc.deps.LeaseRepo = lease
	svc.deps.CheckpointStore = streamResumableCheckpointStore{}

	sub, err := svc.OpenStreamSubscription(context.Background(), "agent-1", ExecRequest{},
		ExecMeta{TenantID: "t1", ExecutionID: "e1", Generation: 3},
		StreamSubscriptionConfig{Heartbeat: time.Hour, IdleExit: time.Hour, ReadBlock: 5})
	if err != nil {
		t.Fatalf("OpenStreamSubscription: %v", err)
	}
	defer sub.Close()

	// 租约有效时**不得**发起抢占：抢占会把正在跑的 runner fence 掉。
	if lease.claimCall != 0 {
		t.Fatalf("ClaimLease called %d times on an active lease, want 0", lease.claimCall)
	}
}

func TestOpenStreamSubscriptionRequiresCheckpointForResume(t *testing.T) {
	store := &fakeStreamStore{block: 5 * time.Millisecond}
	svc := NewAgentService(AgentServiceDeps{})
	svc.deps.StreamStore = store
	svc.deps.ControlBus = &fakeControlBus{}
	svc.deps.LeaseRepo = &fakeLeaseRepo{}
	svc.deps.CheckpointStore = &streamNoopCheckpointStore{}

	// 带 execution_id 但租户限定查询查不到 → 404 语义，且不区分
	// 「不存在」与「不属于你」（spec D4）。
	if _, err := svc.OpenStreamSubscription(context.Background(), "agent-1", ExecRequest{},
		ExecMeta{TenantID: "t1", ExecutionID: "ghost"},
		StreamSubscriptionConfig{Heartbeat: time.Hour, IdleExit: time.Hour}); err == nil {
		t.Fatal("expected error for execution_id without a tenant-scoped checkpoint")
	}
}
