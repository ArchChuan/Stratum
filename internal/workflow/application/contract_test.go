package application

import (
	"context"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/domain/port"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// contractTestStore 是传参契约路径的最小存储桩：只实现 checkpointAttempt 需要的
// SaveAttempt + AppendEvent，其余接口方法由嵌入的 nil 接口兜底（调用即 panic，
// 测试路径不会触达）。
type contractTestStore struct {
	port.RunRepository
	port.AttemptRepository
	port.EventRepository
	attempts map[string][]domain.NodeAttempt
	events   []domain.Event
}

func (s *contractTestStore) SaveAttempt(_ context.Context, _ string, attempt domain.NodeAttempt) error {
	s.attempts[attempt.RunID] = append(s.attempts[attempt.RunID], attempt)
	return nil
}

func (s *contractTestStore) AppendEvent(_ context.Context, _ string, event domain.Event) (domain.Event, error) {
	s.events = append(s.events, event)
	return event, nil
}

func newContractRunService(store *contractTestStore) *RunService {
	return NewRunServiceWithRegistry(nil, store, nil, func() string { return "evt-1" }, zap.NewNop())
}

func succeededAttempt(nodeID, summary string) domain.NodeAttempt {
	return domain.NodeAttempt{RunID: "run-1", NodeID: nodeID, AttemptNo: 1, Status: domain.AttemptStatusSucceeded, OutputSummary: summary}
}

func refSpec() domain.Spec {
	return domain.Spec{Nodes: []domain.Node{
		{ID: "A", Type: domain.NodeTypeAgent, AgentID: "agent-a"},
		{ID: "B", Type: domain.NodeTypeAgent, AgentID: "agent-b"},
	}, Edges: []domain.Edge{{From: "A", To: "B"}}}
}

func TestReferencedOutputKeysCollectsKeyedReferences(t *testing.T) {
	spec := refSpec()
	spec.Nodes[1].InputMapping = map[string]string{
		"summary": "nodes.A.output.summary",
		"title":   "nodes.A.output.title",
		"dup":     "nodes.A.output.summary", // 同一字段重复引用应去重
	}
	refs := referencedOutputKeys(spec)
	require.Equal(t, []string{"summary", "title"}, refs["A"])
}

func TestReferencedOutputKeysIgnoresNonKeyedReferences(t *testing.T) {
	spec := refSpec()
	spec.Nodes[1].InputMapping = map[string]string{
		"whole": "nodes.A.output", // 整段引用不触发契约注入
		"input": "input",
	}
	require.Empty(t, referencedOutputKeys(spec))
}

func TestInjectOutputContractAppendsJSONRequirement(t *testing.T) {
	refs := map[string][]string{"B": {"title", "summary"}}
	injected := injectOutputContract(refs, domain.Node{ID: "B"}, "请分析")
	require.Contains(t, injected, "[系统要求] 请以合法 JSON 对象输出以下字段：summary, title。")
	require.True(t, strings.HasSuffix(injected, "只输出 JSON，不要额外解释或包裹 markdown 代码块。"))
}

func TestInjectOutputContractHandlesEmptyInput(t *testing.T) {
	refs := map[string][]string{"B": {"x"}}
	require.Equal(t, "[系统要求] 请以合法 JSON 对象输出以下字段：x。只输出 JSON，不要额外解释或包裹 markdown 代码块。", injectOutputContract(refs, domain.Node{ID: "B"}, ""))
}

func TestInjectOutputContractLeavesUnreferencedInputUntouched(t *testing.T) {
	// 无 keyed 引用时原样返回，保证存量运行行为完全不变。
	require.Equal(t, "hello", injectOutputContract(map[string][]string{}, domain.Node{ID: "B"}, "hello"))
	require.Equal(t, "", injectOutputContract(nil, domain.Node{ID: "B"}, ""))
}

func TestNodeInputExtractsOutputField(t *testing.T) {
	run := &domain.Run{ID: "run-1", Snapshot: refSpec()}
	states := map[string]domain.NodeAttempt{"A": succeededAttempt("A", `{"summary":"x","title":"y"}`)}
	node := domain.Node{ID: "B", InputMapping: map[string]string{"summary": "nodes.A.output.summary"}}
	input, missing, err := nodeInput(run, node, states)
	require.NoError(t, err)
	require.Empty(t, missing)
	require.JSONEq(t, `{"summary":"x"}`, input)
}

func TestNodeInputReportsMissingOutputField(t *testing.T) {
	run := &domain.Run{ID: "run-1", Snapshot: refSpec()}
	states := map[string]domain.NodeAttempt{"A": succeededAttempt("A", `{"summary":"x"}`)}
	node := domain.Node{ID: "B", InputMapping: map[string]string{
		"summary": "nodes.A.output.summary",
		"missing": "nodes.A.output.absent",
	}}
	input, missing, err := nodeInput(run, node, states)
	require.NoError(t, err)
	require.Contains(t, missing, "A")
	require.JSONEq(t, `{"summary":"x"}`, input)
}

func TestNodeInputReportsInvalidUpstreamJSON(t *testing.T) {
	run := &domain.Run{ID: "run-1", Snapshot: refSpec()}
	states := map[string]domain.NodeAttempt{"A": succeededAttempt("A", "not-json")}
	node := domain.Node{ID: "B", InputMapping: map[string]string{"summary": "nodes.A.output.summary"}}
	_, missing, err := nodeInput(run, node, states)
	require.NoError(t, err)
	require.Contains(t, missing, "A")
	require.Contains(t, missing["A"], "not a valid JSON object")
}

func TestNodeInputPassesThroughWholeOutputReference(t *testing.T) {
	// 整段引用 nodes.A.output 保持历史行为：透传上游整段 OutputSummary 字符串。
	run := &domain.Run{ID: "run-1", Snapshot: refSpec()}
	states := map[string]domain.NodeAttempt{"A": succeededAttempt("A", `{"summary":"x"}`)}
	node := domain.Node{ID: "B", InputMapping: map[string]string{"whole": "nodes.A.output"}}
	input, missing, err := nodeInput(run, node, states)
	require.NoError(t, err)
	require.Empty(t, missing)
	require.JSONEq(t, `{"whole":"{\"summary\":\"x\"}"}`, input)
}

func TestRetryUpstreamOutputRetriesAgentOnContractViolation(t *testing.T) {
	run := &domain.Run{ID: "run-1", Snapshot: refSpec()}
	states := map[string]domain.NodeAttempt{"A": succeededAttempt("A", "not-json")}
	store := &contractTestStore{attempts: map[string][]domain.NodeAttempt{}}
	svc := newContractRunService(store)

	require.NoError(t, svc.retryUpstreamOutput(context.Background(), "tenant-1", run, states, "A", "upstream output is not a valid JSON object"))

	saved := store.attempts["run-1"]
	require.Len(t, saved, 1)
	require.Equal(t, domain.AttemptStatusRetryWait, saved[0].Status)
	require.Equal(t, "upstream_output_contract", saved[0].ErrorCode)
	require.NotNil(t, saved[0].RetryAt)

	types := make([]string, 0, len(store.events))
	for _, e := range store.events {
		types = append(types, e.Type)
	}
	require.Contains(t, types, "workflow.node_retrying")
}

func TestRetryUpstreamOutputHonorsLargerExplicitBudget(t *testing.T) {
	// 作者显式配置 MaxAttempts=5 > 内置预算 3：AttemptNo=3 仍可重试。
	spec := refSpec()
	spec.Nodes[0].Retry = domain.RetryPolicy{MaxAttempts: 5}
	run := &domain.Run{ID: "run-1", Snapshot: spec}
	states := map[string]domain.NodeAttempt{"A": {RunID: "run-1", NodeID: "A", AttemptNo: 3, Status: domain.AttemptStatusSucceeded, OutputSummary: "not-json"}}
	store := &contractTestStore{attempts: map[string][]domain.NodeAttempt{}}
	svc := newContractRunService(store)

	require.NoError(t, svc.retryUpstreamOutput(context.Background(), "tenant-1", run, states, "A", "missing"))
	require.Equal(t, domain.AttemptStatusRetryWait, store.attempts["run-1"][0].Status)
}

func TestRetryUpstreamOutputExhaustsBuiltinBudgetThenFails(t *testing.T) {
	// 内置预算 3 耗尽（AttemptNo=3）后不再重试，落成失败并返回错误。
	run := &domain.Run{ID: "run-1", Snapshot: refSpec()}
	states := map[string]domain.NodeAttempt{"A": {RunID: "run-1", NodeID: "A", AttemptNo: 3, Status: domain.AttemptStatusSucceeded, OutputSummary: "not-json"}}
	store := &contractTestStore{attempts: map[string][]domain.NodeAttempt{}}
	svc := newContractRunService(store)

	err := svc.retryUpstreamOutput(context.Background(), "tenant-1", run, states, "A", "missing")
	require.Error(t, err)
	require.Equal(t, domain.AttemptStatusFailed, store.attempts["run-1"][0].Status)
}

func TestRetryUpstreamOutputRefusesNonAgentUpstream(t *testing.T) {
	// 作者错误引用了非 agent/skill 节点的输出字段：不能重试，run 直接 fail。
	spec := domain.Spec{Nodes: []domain.Node{
		{ID: "mcp", Type: domain.NodeTypeMCPTool, MCPServerID: "srv", MCPToolName: "tool"},
		{ID: "B", Type: domain.NodeTypeAgent, AgentID: "agent-b"},
	}, Edges: []domain.Edge{{From: "mcp", To: "B"}}}
	run := &domain.Run{ID: "run-1", Snapshot: spec}
	states := map[string]domain.NodeAttempt{"mcp": succeededAttempt("mcp", `{"ok":true}`)}
	store := &contractTestStore{attempts: map[string][]domain.NodeAttempt{}}
	svc := newContractRunService(store)

	err := svc.retryUpstreamOutput(context.Background(), "tenant-1", run, states, "mcp", "missing")
	require.Error(t, err)
	require.Contains(t, err.Error(), "is not agent or skill")
}
