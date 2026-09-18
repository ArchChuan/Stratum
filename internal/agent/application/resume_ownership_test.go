package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"go.uber.org/zap"
)

// resumeOwnershipCheckpointStore 记录 ResumeExecution 归属校验期间对 checkpoint
// 存储的全部访问。零调用断言才有判别力：只断言错误值会把「先改了状态再报错」
// 这类写副作用漏过去。
type resumeOwnershipCheckpointStore struct {
	streamNoopCheckpointStore
	cp *domain.AgentExecutionCheckpoint

	getCalls     int
	updateStatus int
	upserts      int
}

func (s *resumeOwnershipCheckpointStore) GetLatest(context.Context, string, string) (*domain.AgentExecutionCheckpoint, error) {
	s.getCalls++
	return s.cp, nil
}

func (s *resumeOwnershipCheckpointStore) UpdateStatus(context.Context, string, string, string) error {
	s.updateStatus++
	return nil
}

func (s *resumeOwnershipCheckpointStore) Upsert(context.Context, string, domain.AgentExecutionCheckpoint) error {
	s.upserts++
	return nil
}

// resumeProbeRepo 统计 Registry.Get 的调用次数，作为「是否真的进入了 Execute」
// 的探针——归属校验必须在任何执行副作用之前短路。
type resumeProbeRepo struct {
	registryAgentRepoFake
	gets int
}

func (r *resumeProbeRepo) Get(ctx context.Context, id string) (*domain.AgentConfig, bool, error) {
	r.gets++
	return r.registryAgentRepoFake.Get(ctx, id)
}

// 裁定 9：checkpoint 的 user_id 会被 Upsert 覆写（ON CONFLICT DO UPDATE SET），
// 因此 resume 不校验归属就等于把 stop 的所有权转移给调用者。归属判定与
// StopExecution 同源 fail closed：两侧都非空且严格相等才放行，且必须先于
// UpdateStatus 这类写副作用。
func TestResumeExecutionEnforcesOwnershipBeforeWrite(t *testing.T) {
	cases := []struct {
		name        string
		checkpoint  *domain.AgentExecutionCheckpoint
		userID      string
		wantErr     error
		wantProceed bool
	}{
		{
			name:        "owner resumes own execution",
			checkpoint:  &domain.AgentExecutionCheckpoint{ExecutionID: "e1", UserID: "u1"},
			userID:      "u1",
			wantProceed: true,
		},
		{
			name:       "foreign user cannot resume another user's execution",
			checkpoint: &domain.AgentExecutionCheckpoint{ExecutionID: "e1", UserID: "u1"},
			userID:     "u2",
			wantErr:    ErrNotFound,
		},
		{
			name:       "empty actor cannot resume a named owner's execution",
			checkpoint: &domain.AgentExecutionCheckpoint{ExecutionID: "e1", UserID: "u1"},
			userID:     "",
			wantErr:    ErrNotFound,
		},
		{
			name:       "ownerless checkpoint is resumable by nobody",
			checkpoint: &domain.AgentExecutionCheckpoint{ExecutionID: "e1"},
			userID:     "u1",
			wantErr:    ErrNotFound,
		},
		{
			name:    "missing checkpoint is not found",
			userID:  "u1",
			wantErr: ErrNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &resumeOwnershipCheckpointStore{cp: tc.checkpoint}
			repo := &resumeProbeRepo{}
			svc := NewAgentService(AgentServiceDeps{
				CheckpointStore: store,
				Registry:        NewRegistry(repo, zap.NewNop()),
			})

			_, _, err := svc.ResumeExecution(
				context.Background(), "a1", ExecRequest{Query: "hi", UserID: tc.userID},
				ExecMeta{TenantID: "t1"}, "e1")

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				// 越权分支不得取得任何写副作用，也不得启动执行。
				if store.updateStatus != 0 {
					t.Fatalf("UpdateStatus calls = %d, want 0（授权必须先于状态变更）", store.updateStatus)
				}
				if repo.gets != 0 {
					t.Fatalf("Registry.Get calls = %d, want 0（越权不得进入 Execute）", repo.gets)
				}
				if store.upserts != 0 {
					t.Fatalf("Upsert calls = %d, want 0（越权不得写 checkpoint）", store.upserts)
				}
				return
			}

			// 正路：放行后必须先改状态、再进入执行。执行本身因 repo 无该 agent 而
			// 返回 ErrNotFound，不影响「已通过归属闸门」的判定。
			if !tc.wantProceed {
				t.Fatal("用例声明了放行却未设置 wantProceed")
			}
			if store.updateStatus != 1 {
				t.Fatalf("UpdateStatus calls = %d, want 1", store.updateStatus)
			}
			if repo.gets != 1 {
				t.Fatalf("Registry.Get calls = %d, want 1（放行后应进入 Execute）", repo.gets)
			}
		})
	}
}
