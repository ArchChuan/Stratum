package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestParseAgentTypeWireDefaultsToReAct(t *testing.T) {
	// 极端情况：任何 wire 值（含空串与未知值）都归一为 ReActAgent。
	for _, wire := range []string{"", "planning", "weird-type"} {
		require.Equal(t, domain.ReActAgent, agent.ParseAgentTypeWireForTest(wire))
	}
}

func TestExecutionSubjectPrefersConversationID(t *testing.T) {
	// 极端情况：ConversationID 优先于 TraceID。
	meta := agent.ExecMeta{TraceID: "trace-1"}
	require.Equal(t, "trace-1", agent.ExecutionSubjectForTest(agent.ExecRequest{}, meta))
	req := agent.ExecRequest{ConversationID: "conv-9"}
	require.Equal(t, "conv-9", agent.ExecutionSubjectForTest(req, meta))
}

func TestApplyAgentAssignment(t *testing.T) {
	// 极端情况：空 RevisionID → no-op，map 保持 nil。
	meta := &agent.ExecMeta{}
	agent.ApplyAgentAssignmentForTest(meta, "a1", port.AgentRevisionAssignment{})
	require.Nil(t, meta.EvolutionTrace.ResourceManifest)

	// 有 revision：manifest 记录 agent:ID → revision。
	meta = &agent.ExecMeta{}
	agent.ApplyAgentAssignmentForTest(meta, "a1", port.AgentRevisionAssignment{RevisionID: "r1"})
	require.Equal(t, "r1", meta.EvolutionTrace.ResourceManifest["agent:a1"])

	// 极端情况：带 experiment → assignments + 顶层 experiment 字段填充一次。
	meta = &agent.ExecMeta{}
	agent.ApplyAgentAssignmentForTest(meta, "a1", port.AgentRevisionAssignment{
		RevisionID: "r2", ExperimentID: "exp-7", Variant: "v2",
	})
	require.Equal(t, "exp-7", meta.EvolutionTrace.ExperimentAssignments["agent:a1"].ExperimentID)
	require.Equal(t, "exp-7", meta.EvolutionTrace.ExperimentID)
	require.Equal(t, "v2", meta.EvolutionTrace.Variant)
	// 已有顶层 experiment 不覆盖。
	agent.ApplyAgentAssignmentForTest(meta, "a2", port.AgentRevisionAssignment{
		RevisionID: "r3", ExperimentID: "exp-8", Variant: "v9",
	})
	require.Equal(t, "exp-7", meta.EvolutionTrace.ExperimentID)
	require.Equal(t, "exp-8", meta.EvolutionTrace.ExperimentAssignments["agent:a2"].ExperimentID)
}

func TestHasFailedAssistantArtifact(t *testing.T) {
	// 极端情况：nil result 安全。
	require.False(t, agent.HasFailedAssistantArtifactForTest(nil))
	// 全部 success → false。
	ok := &domain.AgentResult{AssistantToolArtifacts: []domain.SystemAssistantToolArtifact{{Outcome: "success"}}}
	require.False(t, agent.HasFailedAssistantArtifactForTest(ok))
	// 任一非 success → true。
	failed := &domain.AgentResult{AssistantToolArtifacts: []domain.SystemAssistantToolArtifact{
		{Outcome: "success"}, {Outcome: "failure"},
	}}
	require.True(t, agent.HasFailedAssistantArtifactForTest(failed))
}

func TestBoundedAssistantRoleClass(t *testing.T) {
	require.Equal(t, "admin", agent.BoundedAssistantRoleClassForTest("admin"))
	require.Equal(t, "member", agent.BoundedAssistantRoleClassForTest("member"))
	require.Equal(t, "owner", agent.BoundedAssistantRoleClassForTest("owner"))
	require.Equal(t, "unknown", agent.BoundedAssistantRoleClassForTest(""))
}

func TestNormalizeMCPToolFillsDefaults(t *testing.T) {
	tool := agent.NormalizeMCPToolForTest(port.ToolDefinition{Name: "search"}, "server-1")
	require.Equal(t, domain.ProviderTypeMCP, tool.ProviderType)
	require.Equal(t, "server-1", tool.ProviderID)
	require.Equal(t, "server-1", tool.ServerID)
	require.Equal(t, "search", tool.CapabilityID)
	require.Equal(t, domain.ObservationTypeMCP, tool.NodeType)
	require.NotNil(t, tool.Metadata)
	// 已有值保留。
	tool = agent.NormalizeMCPToolForTest(port.ToolDefinition{
		Name: "x", ProviderType: "custom", ProviderID: "p", ServerID: "s", CapabilityID: "c",
		NodeType: "n", Metadata: map[string]any{"k": "v"},
	}, "server-9")
	require.Equal(t, "custom", tool.ProviderType)
	require.Equal(t, "p", tool.ProviderID)
	require.Equal(t, "v", tool.Metadata["k"])
}

func TestPlatformMCPRiskAndToolRiskRank(t *testing.T) {
	require.Equal(t, 3, agent.ToolRiskRankForTest(port.ToolRiskDestructive))
	require.Equal(t, 2, agent.ToolRiskRankForTest(port.ToolRiskWriteReversible))
	require.Equal(t, 1, agent.ToolRiskRankForTest(port.ToolRiskRead))
	require.Equal(t, 0, agent.ToolRiskRankForTest(port.ToolRiskUnclassified))
	require.Equal(t, 0, agent.ToolRiskRankForTest(""))

	// stricter 取更严格侧。
	require.Equal(t, port.ToolRiskWriteReversible, agent.StricterToolRiskForTest(port.ToolRiskRead, port.ToolRiskWriteReversible))
	require.Equal(t, port.ToolRiskRead, agent.StricterToolRiskForTest(port.ToolRiskRead, port.ToolRiskUnclassified))
	require.Equal(t, port.ToolRiskDestructive, agent.StricterToolRiskForTest(port.ToolRiskDestructive, port.ToolRiskWriteReversible))
}

func TestTruncateRunes(t *testing.T) {
	require.Equal(t, "abc", agent.TruncateRunesForTest("abc", 10))
	require.Equal(t, "ab", agent.TruncateRunesForTest("abc", 2))
	// 极端情况：多字节 rune 不被截断成半字符。
	require.Equal(t, "你好", agent.TruncateRunesForTest("你好世界", 2))
	require.Equal(t, "你", agent.TruncateRunesForTest("你好", 1))
}

func TestExecutionIDOrNew(t *testing.T) {
	// 极端情况：空 id → 生成 v7 UUID。
	got := agent.ExecutionIDOrNewForTest("")
	require.Len(t, got, 36)
	require.Equal(t, "7", got[14:15]) // v7 版本位
	// 非空 → 原样返回。
	require.Equal(t, "exec-1", agent.ExecutionIDOrNewForTest("exec-1"))
}

func TestApplySkillAssignments(t *testing.T) {
	catalog := map[string]port.SkillActivation{"skill-1": {Name: "s1"}}
	agent.ApplySkillAssignmentsForTest(catalog, map[string]port.SkillRevisionAssignment{
		"skill-1": {RevisionID: "r1", ExperimentID: "exp-2", Variant: "v3"},
	})
	act := catalog["skill-1"]
	require.Equal(t, "skill-1", act.SkillID)
	require.Equal(t, "r1", act.RevisionID)
	require.Equal(t, "exp-2", act.ExperimentID)
	require.Equal(t, "v3", act.Variant)
}

// fakeEvidenceProvider 实现 TraceEvidenceProvider 的可脚本化 stub。
type fakeEvidenceProvider struct {
	records []domain.ExecutionRecord
	total   int64
	userID  string
	err     error
}

func (f *fakeEvidenceProvider) ListExecutions(context.Context, string, domain.ListOptions) ([]domain.ExecutionRecord, int64, error) {
	return f.records, f.total, f.err
}

func (f *fakeEvidenceProvider) ToolObservations(context.Context, string, string) ([]domain.ToolObservation, error) {
	return nil, f.err
}

func (f *fakeEvidenceProvider) TraceEvents(context.Context, string, string) ([]domain.AgentTraceEvent, error) {
	return nil, f.err
}

func (f *fakeEvidenceProvider) Resolve(context.Context, string, string) (domain.TraceEvidence, error) {
	if f.err != nil {
		return domain.TraceEvidence{}, f.err
	}
	return domain.TraceEvidence{UserID: f.userID}, nil
}

func (f *fakeEvidenceProvider) ResolveBatch(context.Context, string, []string) (map[string]domain.TraceEvidence, error) {
	return nil, f.err
}

func serviceWithEvidence(provider port.TraceEvidenceProvider) *agent.AgentService {
	return agent.NewAgentService(agent.AgentServiceDeps{
		Registry:         agent.NewRegistry(new(mockAgentRepo), agent.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		EvidenceProvider: provider,
		Logger:           zap.NewNop(),
	})
}

func TestAgentServiceEvidenceMethodsFailClosed(t *testing.T) {
	// 极端情况：EvidenceProvider 缺失 → 全部 fail closed。
	svc := agent.NewAgentService(agent.AgentServiceDeps{Logger: zap.NewNop()})
	_, _, err := svc.ListExecutions(context.Background(), "t1", "u1", 1, 10)
	require.ErrorIs(t, err, domain.ErrEvidenceUnavailable)
	_, err = svc.ListToolTraces(context.Background(), "t1", "u1", "trace-1")
	require.ErrorIs(t, err, domain.ErrEvidenceUnavailable)
	_, err = svc.ListTraceEvents(context.Background(), "t1", "u1", "trace-1")
	require.ErrorIs(t, err, domain.ErrEvidenceUnavailable)
}

func TestAgentServiceListExecutions(t *testing.T) {
	created := time.Now()
	svc := serviceWithEvidence(&fakeEvidenceProvider{
		records: []domain.ExecutionRecord{{
			ID: "e1", TraceID: "t1", AgentID: "a1", AgentName: "Research", UserID: "u1",
			Status: "completed", InputPreview: "in", OutputPreview: "out", TotalTokens: 42,
			DurationMs: 100, CreatedAt: created,
		}},
		total: 1,
	})
	rows, total, err := svc.ListExecutions(context.Background(), "t1", "u1", 0, 20)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	require.Equal(t, "Research", rows[0].AgentName)
	require.Equal(t, created.Format("2006-01-02T15:04:05Z07:00"), rows[0].CreatedAt)

	// 极端情况：空记录 → 空 slice 非 nil。
	svc = serviceWithEvidence(&fakeEvidenceProvider{records: nil, total: 0})
	rows, _, err = svc.ListExecutions(context.Background(), "t1", "u1", 0, 20)
	require.NoError(t, err)
	require.NotNil(t, rows)
	require.Empty(t, rows)

	// 极端情况：provider 错误传播。
	svc = serviceWithEvidence(&fakeEvidenceProvider{err: errors.New("db down")})
	_, _, err = svc.ListExecutions(context.Background(), "t1", "u1", 0, 20)
	require.Error(t, err)
}

func TestAgentServiceTraceOwnerAuthorization(t *testing.T) {
	// 极端情况：userID 为空或不匹配 → ErrEvidenceNotFound。
	svc := serviceWithEvidence(&fakeEvidenceProvider{userID: "owner-1"})
	_, err := svc.ListToolTraces(context.Background(), "t1", "intruder", "trace-1")
	require.ErrorIs(t, err, domain.ErrEvidenceNotFound)
	_, err = svc.ListTraceEvents(context.Background(), "t1", "", "trace-1")
	require.ErrorIs(t, err, domain.ErrEvidenceNotFound)
	// 本人 → 通过。
	_, err = svc.ListToolTraces(context.Background(), "t1", "owner-1", "trace-1")
	require.NoError(t, err)
	_, err = svc.ListTraceEvents(context.Background(), "t1", "owner-1", "trace-1")
	require.NoError(t, err)
}

func TestAgentServiceApprovalMethodsFailClosed(t *testing.T) {
	svc := agent.NewAgentService(agent.AgentServiceDeps{Logger: zap.NewNop()})
	// 极端情况：ApprovalService 缺失 → ListPending 空、Decide/Resume 报错。
	approvals, err := svc.ListPendingApprovals(context.Background(), "t1", "u1", "member")
	require.NoError(t, err)
	require.Empty(t, approvals)
	require.NotNil(t, approvals)
	err = svc.DecideToolApproval(context.Background(), "t1", "a1", "approve", "u1", "ok")
	require.Error(t, err)
	_, _, err = svc.ResumeToolApproval(context.Background(), "t1", "a1")
	require.Error(t, err)
}

// fakeModelContextProvider 让 GetChatModelContextWindow 可脚本化。
type fakeModelContextProvider struct {
	cw  int
	err error
}

func (f fakeModelContextProvider) GetChatModelContextWindow(context.Context, string, string) (int, error) {
	return f.cw, f.err
}

// windowRatioAt 运行时按 DefaultContextWindowRatio 缩放窗口
// （want 值计算：常量浮点→int 转换在编译期被拒绝）。
func windowRatioAt(win int) int {
	return int(float64(win) * constants.DefaultContextWindowRatio)
}

func TestResolveExecutionWindow(t *testing.T) {
	// 执行时两阶段解析：registry > vendor 表 > 8000；显式值按
	// [MinContextWindowTokens, w×0.85] clamp；UNKNOWN 不压显式值（D7）。
	cases := []struct {
		name     string
		provider port.ModelContextProvider
		vendor   func(string) (int, int)
		explicit int
		want     int
		wantSrc  agentgraph.WindowSource
	}{
		// registry 命中 + 显式在 clamp 区间内 → 显式生效。
		{name: "registry explicit within clamp",
			provider: fakeModelContextProvider{cw: 200_000}, explicit: 30_000,
			want: 30_000, wantSrc: agentgraph.WindowExplicit},
		// registry 未知 + vendor 命中 + 未配置 → vendor 窗口 × 0.85。
		{name: "vendor derived",
			provider: fakeModelContextProvider{cw: 0},
			vendor:   func(string) (int, int) { return 131_072, 8192 }, explicit: 0,
			want: windowRatioAt(131_072), wantSrc: agentgraph.WindowRegistry},
		// 全空 → 保守默认。
		{name: "fallback default",
			provider: fakeModelContextProvider{cw: 0}, explicit: 0,
			want: constants.DefaultAgentContextTokens, wantSrc: agentgraph.WindowFallback},
		// 显式超过 w×0.85 → clamp 到 w×0.85。
		{name: "explicit above ratio cap clamps",
			provider: fakeModelContextProvider{cw: 200_000}, explicit: 200_000,
			want: windowRatioAt(200_000), wantSrc: agentgraph.WindowExplicit},
		// registry 窗口超 1M 硬 ceiling → 收敛到 1M 再派生。
		{name: "model window capped at 1M",
			provider: fakeModelContextProvider{cw: 2_000_000}, explicit: 0,
			want:    windowRatioAt(constants.MaxContextWindowTokens),
			wantSrc: agentgraph.WindowRegistry},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := agent.NewAgentService(agent.AgentServiceDeps{
				ModelContextProvider: tc.provider,
				VendorWindowLookup:   tc.vendor,
				Logger:               zap.NewNop(),
			})
			got, src := agent.ResolveExecutionWindowForTest(svc, context.Background(), "t1", "qwen", tc.explicit)
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.wantSrc, src)
		})
	}
}

func TestBaseAgentSettersResetAndMemoryBound(t *testing.T) {
	a := newReActAgent()

	// setter 链式返回自身。
	require.Same(t, a, a.WithMetrics(&observability.NoopMetrics{}))
	require.Same(t, a, a.WithChatStore(&mockChatStore{}))
	a.SetCapGateway(&mockCapGW{})
	a.SetHistoryCompactor(noopCompactor{})
	a.SetCheckpointStore(noopCheckpointStore{})
	a.SetChatStore(&mockChatStore{})
	require.Same(t, a, a.WithCheckpointStore(noopCheckpointStore{}))

	// AddToMemory：超过 100 条截断为最近 100 条。
	for i := 0; i < 105; i++ {
		a.AddToMemory(agent.Message{Role: "user", Content: "m"})
	}
	require.Len(t, a.GetMemory(), 100)

	// 极端情况：Reset 清空 memory 与 state。
	a.State = agent.AgentState{StepsTaken: 3}
	a.Reset()
	require.Empty(t, a.GetMemory())
	require.Equal(t, agent.AgentState{}, a.State)
	require.Equal(t, 0, a.State.StepsTaken)
}

type noopCompactor struct{}

func (noopCompactor) CompactHistory(context.Context, []port.LLMMessage) (string, error) {
	return "", nil
}

type noopCheckpointStore struct{}

func (noopCheckpointStore) Upsert(context.Context, string, domain.AgentExecutionCheckpoint) error {
	return nil
}
func (noopCheckpointStore) GetLatest(context.Context, string, string) (*domain.AgentExecutionCheckpoint, error) {
	return nil, nil
}
func (noopCheckpointStore) MarkCompleted(context.Context, string, string) error        { return nil }
func (noopCheckpointStore) UpdateStatus(context.Context, string, string, string) error { return nil }
func (noopCheckpointStore) DeleteExpired(context.Context, string) (int64, error)       { return 0, nil }
