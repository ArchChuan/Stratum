package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

// snapshot 在锁内复制一份条目快照，避免断言读取与 Append 并发。
func (f *fakeStreamStore) snapshot() []port.StreamEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]port.StreamEntry(nil), f.entries...)
}

func decodeJSONPayload(t *testing.T, payload string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("decode payload %q: %v", payload, err)
	}
	return decoded
}

// 裁定 8（经人类批准的 brief 外新增）：审批待决是**可恢复的暂停**而非失败。
// 审批错误是普通 runErr，若被 appendStreamTerminal 当 error 分流，工具审批就
// 退化成 error 帧，前端审批卡片（ChatStreamContext 的 approval_required 分支）
// 再也不渲染。本用例同时覆盖单条与批量两种错误形态。
func TestAppendStreamTerminalRendersApprovalAsApprovalFrame(t *testing.T) {
	first := port.ToolApprovalRequiredError{
		ApprovalID: "ap-1", ToolCallID: "tc-1", ServerID: "srv-1",
		ToolName: "delete_workspace", RiskLevel: domain.ToolRiskDestructive,
	}
	second := port.ToolApprovalRequiredError{
		ApprovalID: "ap-2", ToolCallID: "tc-2", ServerID: "srv-2",
		ToolName: "drop_table", RiskLevel: domain.ToolRiskWriteReversible,
	}
	batchErr := &port.BatchToolApprovalRequiredError{
		Errors: []port.ToolApprovalRequiredError{first, second},
	}

	cases := []struct {
		name      string
		runErr    error
		approvals []port.ToolApprovalRequiredError
	}{
		{name: "single approval", runErr: &first, approvals: []port.ToolApprovalRequiredError{first}},
		{name: "batch approvals", runErr: batchErr, approvals: batchErr.Errors},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStreamStore{}
			svc := NewAgentService(AgentServiceDeps{})
			svc.deps.StreamStore = store

			// result=nil 且 runErr 非 nil：若审批分支不是最高优先级，这一组合
			// 必然落进 default 的 error 帧——正是本断言要咬住的回归。
			svc.appendStreamTerminal(context.Background(), "e1", 1, nil, tc.runErr)

			entries := store.snapshot()
			if len(entries) != 1 {
				t.Fatalf("wrote %d stream entries, want 1", len(entries))
			}
			got := entries[0]
			if got.Event == port.StreamEventError {
				t.Fatalf("approval pending degraded to error frame: %s", got.Payload)
			}
			if got.Event != port.StreamEventApprovalRequired {
				t.Fatalf("event = %q, want %q", got.Event, port.StreamEventApprovalRequired)
			}
			want := string(ApprovalRequiredPayloadBytes(tc.approvals))
			if got.Payload != want {
				t.Fatalf("payload = %s, want %s", got.Payload, want)
			}
			decoded := decodeJSONPayload(t, got.Payload)
			if decoded["status"] != "waiting_approval" {
				t.Fatalf("status = %v, want waiting_approval", decoded["status"])
			}
			approvals, _ := decoded["approvals"].([]any)
			if len(approvals) != len(tc.approvals) {
				t.Fatalf("approvals len = %d, want %d", len(approvals), len(tc.approvals))
			}
			// 首条镜像：非流式 202 体与偶一帧共用同一形状，旧前端读顶层 approvalId。
			if decoded["approvalId"] != tc.approvals[0].ApprovalID {
				t.Fatalf("top-level approvalId = %v, want %s", decoded["approvalId"], tc.approvals[0].ApprovalID)
			}
		})
	}
}

// 裁定 9 安全红线（经人类批准的 brief 外新增）：PublicErrorMapper 为 nil 时，
// **任何路径**都不得回落到 err.Error()。用带可辨识子串的哨兵模拟上游错误原文，
// 断言这些子串不出现在流内 error 帧里。
func TestErrorPayloadBytesDoesNotLeakRawErrorWithoutMapper(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{})

	raw := fmt.Errorf("dial tcp 10.11.12.13:6379: %w", errors.New("connection refused"))
	payload := string(svc.errorPayloadBytes(raw))

	for _, leak := range []string{"10.11.12.13", "6379", "connection refused", "dial tcp"} {
		if strings.Contains(payload, leak) {
			t.Fatalf("error frame leaks raw error text %q: %s", leak, payload)
		}
	}
	decoded := decodeJSONPayload(t, payload)
	if decoded["error"] != streamGenericErrorMessage {
		t.Fatalf("error = %v, want fail-safe %q", decoded["error"], streamGenericErrorMessage)
	}
	if _, ok := decoded["code"]; ok {
		t.Fatalf("code must be absent without a mapper, got %v", decoded["code"])
	}
}

// 裁定 9：装配 mapper 后其返回的 code 必须原样透出——前端按 code 分支
// （agent.api.ts 消费 code，AgentChatPage 处理 ASSISTANT_MODEL_UNAVAILABLE），
// 丢 code 等于把可编程错误退化成纯文案。
func TestErrorPayloadBytesPreservesMapperCode(t *testing.T) {
	const wantCode = "ASSISTANT_MODEL_UNAVAILABLE"
	svc := NewAgentService(AgentServiceDeps{
		PublicErrorMapper: func(err error) (string, string) {
			if errors.Is(err, domain.ErrAssistantModelUnavailable) {
				return "系统助手模型不可用", wantCode
			}
			return "", ""
		},
	})

	payload := string(svc.errorPayloadBytes(
		fmt.Errorf("resolve assistant model: %w", domain.ErrAssistantModelUnavailable)))
	decoded := decodeJSONPayload(t, payload)
	if decoded["code"] != wantCode {
		t.Fatalf("code = %v, want %s", decoded["code"], wantCode)
	}
	if decoded["error"] != "系统助手模型不可用" {
		t.Fatalf("error = %v, want mapper message", decoded["error"])
	}
}

// 裁定 9：mapper 命中失败（返回空文案）时回落固定文案，而不是 err.Error()。
// 这是「宁可给通用文案，也不给原文」的第二道闸。
func TestErrorPayloadBytesFallsBackToGenericWhenMapperReturnsEmpty(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{
		PublicErrorMapper: func(error) (string, string) { return "", "" },
	})

	payload := string(svc.errorPayloadBytes(
		fmt.Errorf(`pgx: FATAL: password authentication failed for user "stratum"`)))
	if strings.Contains(payload, "password authentication") {
		t.Fatalf("error frame leaks raw error text: %s", payload)
	}
	decoded := decodeJSONPayload(t, payload)
	if decoded["error"] != streamGenericErrorMessage {
		t.Fatalf("error = %v, want fail-safe %q", decoded["error"], streamGenericErrorMessage)
	}
}
