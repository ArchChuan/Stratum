package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// fakeLeaseRepo 是租约端口的内存替身，用于验证三路分支的判定。
type fakeLeaseRepo struct {
	status     port.LeaseStatus
	claimGen   int
	claimErr   error
	stampGen   int
	stampErr   error
	claimCall  int
	statusCall int
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
	f.statusCall++
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

// streamResumableCheckpointOwner 是续跑替身 checkpoint 的归属人。既有 execution 的
// 续跑/接管必须通过归属校验（与 stop 同源 fail closed），因此所有以既有 executionID
// 发起续跑的用例都必须用同一身份，否则会合法地收到 ErrNotFound。
const streamResumableCheckpointOwner = "u1"

// streamResumableCheckpointStore 是「租户限定查询查得到」的替身，用于续跑路径。
// 嵌入 streamNoopCheckpointStore 只为继承其余 8 个方法——本用例只走 GetLatest。
type streamResumableCheckpointStore struct{ streamNoopCheckpointStore }

func (streamResumableCheckpointStore) GetLatest(context.Context, string, string) (*domain.AgentExecutionCheckpoint, error) {
	return &domain.AgentExecutionCheckpoint{UserID: streamResumableCheckpointOwner}, nil
}

// readFrame 有界读取一帧；ok=false 表示通道已关闭（订阅收敛）。
func readFrame(t *testing.T, frames <-chan StreamFrame) (StreamFrame, bool) {
	t.Helper()
	select {
	case f, ok := <-frames:
		return f, ok
	case <-time.After(5 * time.Second):
		t.Fatal("订阅未在 5s 内产出帧：计划/起点实现被改坏会让回放读不到任何条目")
		return StreamFrame{}, false
	}
}

func TestOpenStreamSubscriptionTailsWhenLeaseActive(t *testing.T) {
	store := &fakeStreamStore{block: 5 * time.Millisecond}
	lease := &fakeLeaseRepo{status: port.LeaseStatus{Generation: 3, Active: true}}
	ctx := context.Background()
	// 两条条目：已消费的 token + 终态 done。客户端游标停在 token 上。
	consumedID, err := store.Append(ctx, "e1", 3, port.StreamEntry{
		Event: port.StreamEventToken, Payload: `{"token":"已消费"}`})
	if err != nil {
		t.Fatalf("seed token: %v", err)
	}
	terminalID, err := store.Append(ctx, "e1", 3, port.StreamEntry{
		Event: port.StreamEventDone, Payload: `{"done":true}`})
	if err != nil {
		t.Fatalf("seed done: %v", err)
	}

	svc := NewAgentService(AgentServiceDeps{})
	svc.deps.StreamStore = store
	svc.deps.ControlBus = &fakeControlBus{}
	svc.deps.LeaseRepo = lease
	svc.deps.CheckpointStore = streamResumableCheckpointStore{}

	sub, err := svc.OpenStreamSubscription(ctx, "agent-1", ExecRequest{UserID: streamResumableCheckpointOwner},
		ExecMeta{TenantID: "t1", ExecutionID: "e1", Generation: 3, LastEventID: consumedID},
		StreamSubscriptionConfig{Heartbeat: time.Hour, IdleExit: time.Hour, ReadBlock: 5})
	if err != nil {
		t.Fatalf("OpenStreamSubscription: %v", err)
	}
	defer sub.Close()

	// 起点断言：客户端游标与服务端 generation 相等 → 增量回放，首帧必须是游标
	// **之后**的 done。若实现忽略游标（全量回放）则首帧会是 token；若 generation
	// 取错则 plan 会退化成 reset 帧——两种改坏都会在这里变红。
	first, ok := readFrame(t, sub.Frames())
	if !ok {
		t.Fatal("订阅在回放任何帧之前就关闭了")
	}
	if first.ID != terminalID || first.Event != port.StreamEventDone || first.Data != `{"done":true}` {
		t.Fatalf("首帧 = %+v, want 仅回放游标之后的 done(id=%s)", first, terminalID)
	}
	// 终态帧之后订阅必须收敛，不再产出。
	if extra, ok := readFrame(t, sub.Frames()); ok {
		t.Fatalf("终态帧之后仍产出帧：%+v", extra)
	}

	// 租约有效时**不得**发起抢占：抢占会把正在跑的 runner fence 掉。
	if lease.claimCall != 0 {
		t.Fatalf("ClaimLease called %d times on an active lease, want 0", lease.claimCall)
	}
}

func TestOpenStreamSubscriptionRequiresCheckpointForResume(t *testing.T) {
	store := &fakeStreamStore{block: 5 * time.Millisecond}
	lease := &fakeLeaseRepo{}
	svc := NewAgentService(AgentServiceDeps{})
	svc.deps.StreamStore = store
	svc.deps.ControlBus = &fakeControlBus{}
	svc.deps.LeaseRepo = lease
	svc.deps.CheckpointStore = &streamNoopCheckpointStore{}

	// 带 execution_id 但租户限定查询查不到 → 404 语义，且不区分
	// 「不存在」与「不属于你」（spec D4）。断言到具体 sentinel：只判 err != nil
	// 会把「DB 故障」之类的 5xx 也放行，前端就拿不到 404 而与重复开跑混淆。
	sub, err := svc.OpenStreamSubscription(context.Background(), "agent-1", ExecRequest{},
		ExecMeta{TenantID: "t1", ExecutionID: "ghost"},
		StreamSubscriptionConfig{Heartbeat: time.Hour, IdleExit: time.Hour})
	if sub != nil {
		t.Fatalf("查不到 checkpoint 时不得返回句柄：%+v", sub)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	// checkpoint 闸门必须排在租约操作之前：否则每次未知 execution_id 都会打一次
	// 租约查询，existence oracle 与无谓的 CAS 面都被放大。
	if lease.statusCall != 0 || lease.claimCall != 0 {
		t.Fatalf("租约被访问 %d(status)/%d(claim) 次, want 0/0", lease.statusCall, lease.claimCall)
	}
}

// 裁定 9 同类前提：带 execution_id 的流式续跑与 resume 走同一条覆写路径——
// runner 写入的 checkpoint 会把 user_id 覆写成调用者（Upsert 的
// ON CONFLICT DO UPDATE SET 含 user_id）。既有的 execution 只有其所有者能续跑，
// 否则同租户调用者可拿他人的 execution_id 把所有权转移给自己，stop 的 fail closed
// 随之被绕过。查不到与不属于你一律 ErrNotFound（关闭 existence oracle）。
func TestOpenStreamSubscriptionRejectsForeignOwnerOnResume(t *testing.T) {
	cases := []struct {
		name   string
		userID string
	}{
		{name: "foreign user cannot resume another user's execution", userID: "u2"},
		{name: "empty actor cannot resume a named owner's execution", userID: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStreamStore{block: 5 * time.Millisecond}
			lease := &fakeLeaseRepo{status: port.LeaseStatus{Generation: 3, Active: true}}
			svc := NewAgentService(AgentServiceDeps{})
			svc.deps.StreamStore = store
			svc.deps.ControlBus = &fakeControlBus{}
			svc.deps.LeaseRepo = lease
			svc.deps.CheckpointStore = streamResumableCheckpointStore{}

			sub, err := svc.OpenStreamSubscription(context.Background(), "agent-1",
				ExecRequest{Query: "hi", UserID: tc.userID},
				ExecMeta{TenantID: "t1", ExecutionID: "e1", Generation: 3},
				StreamSubscriptionConfig{Heartbeat: time.Hour, IdleExit: time.Hour, ReadBlock: 5})

			if sub != nil {
				sub.Close()
				t.Fatalf("越权续跑返回了句柄：%+v", sub)
			}
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
			// 归属闸门必须排在任何接管动作之前：否则一次越权请求就能 CAS 抢占
			// 正在跑的 runner 的租约，把它 fence 掉。
			if lease.claimCall != 0 {
				t.Fatalf("ClaimLease called %d times on a foreign resume, want 0", lease.claimCall)
			}
			if lease.statusCall != 0 {
				t.Fatalf("LeaseStatus called %d times on a foreign resume, want 0", lease.statusCall)
			}
		})
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

// I-4：appendStreamTerminal 的 done / stopped / error 三个分支此前零覆盖。
// 每个分支都有一条**改坏即变红**的断言：done 断言 output 透传，stopped 断言
// 逐字形状（前端按 data 字段嗅探，形状即契约），error 断言走公开映射而非原文。
func TestAppendStreamTerminalSelectsFrameByOutcome(t *testing.T) {
	cases := []struct {
		name        string
		result      *AgentResult
		runErr      error
		wantEvent   string
		wantPayload string
	}{
		{
			name:        "success writes done with output",
			result:      &AgentResult{Output: "答案", Steps: 3, TokensUsed: 11},
			wantEvent:   port.StreamEventDone,
			wantPayload: `"output":"答案"`,
		},
		{
			name:        "cancellation writes stopped",
			result:      nil,
			runErr:      fmt.Errorf("agent: run: %w", context.Canceled),
			wantEvent:   port.StreamEventStopped,
			wantPayload: `{"done":true,"stopped":true}`,
		},
		{
			name:      "failure writes error",
			result:    nil,
			runErr:    errors.New("provider exploded"),
			wantEvent: port.StreamEventError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStreamStore{}
			svc := NewAgentService(AgentServiceDeps{})
			svc.deps.StreamStore = store

			svc.appendStreamTerminal(context.Background(), "e1", 1, tc.result, tc.runErr)

			entries := store.snapshot()
			if len(entries) != 1 {
				t.Fatalf("wrote %d stream entries, want 1", len(entries))
			}
			got := entries[0]
			if got.Event != tc.wantEvent {
				t.Fatalf("event = %q, want %q (payload=%s)", got.Event, tc.wantEvent, got.Payload)
			}
			if tc.wantPayload == "" {
				// error 分支：无 mapper 时只能是通用文案，绝不能是上游原文。
				if strings.Contains(got.Payload, "provider exploded") {
					t.Fatalf("error 帧泄漏上游原文：%s", got.Payload)
				}
				if decodeJSONPayload(t, got.Payload)["error"] != streamGenericErrorMessage {
					t.Fatalf("error = %s, want 通用文案", got.Payload)
				}
				return
			}
			if got.Payload != tc.wantPayload {
				// stopped 形状必须逐字命中；done 载荷含 JSON 键序，改用子串定位。
				if tc.wantEvent == port.StreamEventStopped {
					t.Fatalf("stopped 载荷 = %s, want %s", got.Payload, tc.wantPayload)
				}
				if !strings.Contains(got.Payload, tc.wantPayload) {
					t.Fatalf("载荷 = %s, want 含 %s", got.Payload, tc.wantPayload)
				}
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
	payload := string(svc.ErrorPayloadBytes(raw))

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

	payload := string(svc.ErrorPayloadBytes(
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

	payload := string(svc.ErrorPayloadBytes(
		fmt.Errorf(`pgx: FATAL: password authentication failed for user "stratum"`)))
	if strings.Contains(payload, "password authentication") {
		t.Fatalf("error frame leaks raw error text: %s", payload)
	}
	decoded := decodeJSONPayload(t, payload)
	if decoded["error"] != streamGenericErrorMessage {
		t.Fatalf("error = %v, want fail-safe %q", decoded["error"], streamGenericErrorMessage)
	}
}

// stopRecordingControlBus 记录 PublishStop 的调用。归属判定失败时「零调用」才是
// 本组用例要守住的断言——只断言错误值会把「放行但报错」这类缺陷漏过去。
type stopRecordingControlBus struct {
	mu    sync.Mutex
	stops []string
}

func (b *stopRecordingControlBus) PublishStop(_ context.Context, executionID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stops = append(b.stops, executionID)
	return nil
}

func (b *stopRecordingControlBus) PublishViewer(context.Context, string, string) error { return nil }

func (b *stopRecordingControlBus) Subscribe(context.Context, string) (<-chan port.ControlMessage, func(), error) {
	ch := make(chan port.ControlMessage)
	return ch, func() {}, nil
}

func (b *stopRecordingControlBus) stopCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.stops)
}

// stopCheckpointStore 返回固定 checkpoint，覆盖 StopExecution 的归属判定分支；
// 嵌入 streamNoopCheckpointStore 只为继承其余 8 个方法。
//
// 记录收到的 (tenantID, executionID)：红线第 3 条要求租户作用域查询显式携带
// tenant，桩若吞掉入参，删掉 StopExecution 的 tenant 透传不会有任何用例变红。
type stopCheckpointStore struct {
	streamNoopCheckpointStore
	cp      *domain.AgentExecutionCheckpoint
	loadErr error

	gotTenantID    string
	gotExecutionID string
	calls          int
}

func (s *stopCheckpointStore) GetLatest(
	_ context.Context, tenantID, executionID string,
) (*domain.AgentExecutionCheckpoint, error) {
	s.calls++
	s.gotTenantID = tenantID
	s.gotExecutionID = executionID
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return s.cp, nil
}

// 裁定 8：归属判定必须严格相等 fail closed。任一为空都不放行，相等才发布停止。
func TestStopExecutionEnforcesOwnershipFailClosed(t *testing.T) {
	cases := []struct {
		name       string
		checkpoint *domain.AgentExecutionCheckpoint
		userID     string
		wantErr    error
		wantStops  int
	}{
		{
			name:       "owner stops own execution",
			checkpoint: &domain.AgentExecutionCheckpoint{ExecutionID: "e1", UserID: "u1"},
			userID:     "u1",
			wantStops:  1,
		},
		{
			name:       "foreign user cannot stop another user's execution",
			checkpoint: &domain.AgentExecutionCheckpoint{ExecutionID: "e1", UserID: "u1"},
			userID:     "u2",
			wantErr:    ErrNotFound,
			wantStops:  0,
		},
		{
			name:       "empty actor cannot stop a named owner's execution",
			checkpoint: &domain.AgentExecutionCheckpoint{ExecutionID: "e1", UserID: "u1"},
			userID:     "",
			wantErr:    ErrNotFound,
			wantStops:  0,
		},
		{
			name:       "ownerless checkpoint is stoppable by nobody",
			checkpoint: &domain.AgentExecutionCheckpoint{ExecutionID: "e1"},
			userID:     "u1",
			wantErr:    ErrNotFound,
			wantStops:  0,
		},
		{
			name:       "empty actor cannot stop an ownerless checkpoint",
			checkpoint: &domain.AgentExecutionCheckpoint{ExecutionID: "e1"},
			userID:     "",
			wantErr:    ErrNotFound,
			wantStops:  0,
		},
		{
			name:      "missing checkpoint is not found",
			userID:    "u1",
			wantErr:   ErrNotFound,
			wantStops: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := &stopRecordingControlBus{}
			store := &stopCheckpointStore{cp: tc.checkpoint}
			svc := NewAgentService(AgentServiceDeps{
				ControlBus:      bus,
				CheckpointStore: store,
			})

			err := svc.StopExecution(context.Background(), "t1", "e1", tc.userID)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if got := bus.stopCount(); got != tc.wantStops {
				t.Fatalf("PublishStop calls = %d, want %d", got, tc.wantStops)
			}
			// 归属判定的输入来自租户限定查询：tenantID 必须逐字透传，executionID
			// 必须是被请求的那条，不能查错行。
			if store.calls != 1 {
				t.Fatalf("checkpoint lookups = %d, want 1", store.calls)
			}
			if store.gotTenantID != "t1" || store.gotExecutionID != "e1" {
				t.Fatalf("checkpoint lookup = (%q,%q), want (t1,e1)", store.gotTenantID, store.gotExecutionID)
			}
			// 发布点必须是请求的那条执行，且归属失败时不得发布。
			for _, id := range bus.stops {
				if id != "e1" {
					t.Fatalf("PublishStop(%q), want e1", id)
				}
			}
		})
	}
}

// 基础设施故障必须向上传播，不得静默降级成 ErrNotFound（否则一次控制通道未装配
// 或 DB 抖动会被前端读成「查无此执行」）。两个分支都返回普通 error，不匹配任何
// sentinel，由 handler 映射成 5xx。
func TestStopExecutionSurfacesInfrastructureFailures(t *testing.T) {
	cases := []struct {
		name string
		deps AgentServiceDeps
	}{
		{
			name: "control bus not configured",
			deps: AgentServiceDeps{CheckpointStore: &stopCheckpointStore{
				cp: &domain.AgentExecutionCheckpoint{ExecutionID: "e1", UserID: "u1"}}},
		},
		{
			name: "checkpoint lookup fails",
			deps: AgentServiceDeps{
				ControlBus:      &stopRecordingControlBus{},
				CheckpointStore: &stopCheckpointStore{loadErr: errors.New("checkpoint store down")},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewAgentService(tc.deps)
			err := svc.StopExecution(context.Background(), "t1", "e1", "u1")
			if err == nil {
				t.Fatal("err = nil, want an infrastructure error")
			}
			if errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v: infrastructure failure must not masquerade as ErrNotFound", err)
			}
		})
	}
}
