package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"go.uber.org/zap"
)

// 裁定 10：meta.ExecutionID 客户端可控，而 Execute 会据此
// （a）resumeFromCheckpoint 读走他人 messages/plan 快照（跨用户状态泄露），
// （b）Upsert 时借 ON CONFLICT 的 user_id = EXCLUDED.user_id 把归属改写成调用者，
// 使调用者自此满足 stop 的所有权判定。闸门必须与 stop/resume 同源：两侧都非空且
// 严格相等才放行，且必须落在任何副作用（含 resumeFromCheckpoint）之前。
func TestExecuteEnforcesOwnershipFailClosed(t *testing.T) {
	const owner = "u1"

	cases := []struct {
		name string
		// noStore 为 true 时 CheckpointStore 整体不装配（闸门跳过分支）。
		noStore     bool
		checkpoint  *domain.AgentExecutionCheckpoint
		executionID string
		userID      string
		wantErr     error
		wantProceed bool
		// wantGetLatest 钉住闸门是否真的去查了行，防止「闸门被短路」这类回归。
		wantGetLatest int
	}{
		{
			name:          "foreign actor cannot resume another user's execution via execute",
			checkpoint:    &domain.AgentExecutionCheckpoint{ExecutionID: "e1", UserID: owner},
			executionID:   "e1",
			userID:        "u2",
			wantErr:       ErrNotFound,
			wantGetLatest: 1,
		},
		{
			name:          "empty actor cannot resume a named owner's execution",
			checkpoint:    &domain.AgentExecutionCheckpoint{ExecutionID: "e1", UserID: owner},
			executionID:   "e1",
			userID:        "",
			wantErr:       ErrNotFound,
			wantGetLatest: 1,
		},
		{
			name:          "ownerless checkpoint is resumable by nobody",
			checkpoint:    &domain.AgentExecutionCheckpoint{ExecutionID: "e1"},
			executionID:   "e1",
			userID:        owner,
			wantErr:       ErrNotFound,
			wantGetLatest: 1,
		},
		{
			name:          "unknown execution id is not found",
			executionID:   "ghost",
			userID:        owner,
			wantErr:       ErrNotFound,
			wantGetLatest: 1,
		},
		{
			name:          "owner proceeds past the gate",
			checkpoint:    &domain.AgentExecutionCheckpoint{ExecutionID: "e1", UserID: owner},
			executionID:   "e1",
			userID:        owner,
			wantProceed:   true,
			wantGetLatest: 1,
		},
		{
			name:        "new execution skips the gate",
			executionID: "",
			userID:      owner,
			wantProceed: true,
		},
		{
			name:        "gate is skipped when no checkpoint store is wired",
			noStore:     true,
			executionID: "e1",
			userID:      owner,
			wantProceed: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 探针：Registry.Get 一旦被调用就说明闸门放行、确实进了执行链。只断言
			// 错误值会被「Execute 自身返回的错误」骗过（同域 resume 用例同一手法）。
			repo := &resumeProbeRepo{}
			deps := AgentServiceDeps{Registry: NewRegistry(repo, zap.NewNop())}
			var store *resumeOwnershipCheckpointStore
			if !tc.noStore {
				store = &resumeOwnershipCheckpointStore{cp: tc.checkpoint}
				deps.CheckpointStore = store
			}
			svc := NewAgentService(deps)

			_, _, err := svc.Execute(context.Background(), "a1",
				ExecRequest{Query: "hi", UserID: tc.userID},
				ExecMeta{TenantID: "t1", ExecutionID: tc.executionID})

			if store != nil && store.getCalls != tc.wantGetLatest {
				t.Fatalf("checkpoint lookups = %d, want %d", store.getCalls, tc.wantGetLatest)
			}
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				// 越权分支不得取得任何写副作用，也不得进入执行（因此也不会
				// resumeFromCheckpoint 读走他人快照）。
				if repo.gets != 0 {
					t.Fatalf("Registry.Get calls = %d, want 0（越权不得进入执行链）", repo.gets)
				}
				if store.updateStatus != 0 {
					t.Fatalf("UpdateStatus calls = %d, want 0", store.updateStatus)
				}
				if store.upserts != 0 {
					t.Fatalf("Upsert calls = %d, want 0（越权不得写 checkpoint）", store.upserts)
				}
				return
			}

			if !tc.wantProceed {
				t.Fatal("用例声明了放行却未设置 wantProceed")
			}
			// 放行后必须真的进入执行链（后续因 fake repo 无该 agent 而失败，
			// 与本用例判定的归宿无关）。
			if repo.gets != 1 {
				t.Fatalf("Registry.Get calls = %d, want 1（放行后应进入执行链）", repo.gets)
			}
		})
	}
}
