package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/domain/port"
	"github.com/byteBuilderX/stratum/pkg/dag"
)

// deterministicExecutionID 生成 agent 节点在该 run 内确定性的执行 ID。首次执行与
// 审批后/暂停后重跑一致，保证 agent checkpoint 的 execution_id 命中同一恢复键
// （maybeResumeApproval / resumeFromCheckpoint 依赖）。
func deterministicExecutionID(runID, nodeID string) string {
	return "wf:" + runID + ":" + nodeID
}

func commandHash(versionID string, input map[string]any) (string, error) {
	payload, err := json.Marshal(struct {
		VersionID string         `json:"version_id"`
		Input     map[string]any `json:"input"`
	}{VersionID: versionID, Input: input})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func latestAttempts(attempts []domain.NodeAttempt) map[string]domain.NodeAttempt {
	out := make(map[string]domain.NodeAttempt)
	for _, attempt := range attempts {
		if current, ok := out[attempt.NodeID]; !ok || attempt.AttemptNo > current.AttemptNo {
			out[attempt.NodeID] = attempt
		}
	}
	return out
}

func nextAttemptNo(attempts []domain.NodeAttempt, nodeID string) int {
	next := 1
	for _, attempt := range attempts {
		if attempt.NodeID == nodeID && attempt.AttemptNo >= next {
			next = attempt.AttemptNo + 1
		}
	}
	return next
}

// readyDecision 描述单个节点在当前状态的下一步：进入 ready/skipped 集合、
// 标记终态、等待阻塞，或由错误终止整个 readySet 求值。
type readyDecision int

const (
	decisionPending readyDecision = iota
	decisionReady
	decisionSkipped
	decisionTerminal
	decisionFailed
)

func readySet(spec domain.Spec, states map[string]domain.NodeAttempt) (ready, skipped []domain.Node, complete bool, err error) {
	if !hasConditionalRouting(spec) {
		return readySetFromKernel(spec, states, time.Now())
	}
	incoming := make(map[string][]domain.Edge, len(spec.Nodes))
	byID := make(map[string]domain.Node, len(spec.Nodes))
	for _, node := range spec.Nodes {
		byID[node.ID] = node
	}
	for _, edge := range spec.Edges {
		incoming[edge.To] = append(incoming[edge.To], edge)
	}
	terminal := 0
	for _, node := range spec.Nodes {
		state, exists := states[node.ID]
		decision, err := nodeReadyDecision(node, state, exists, incoming[node.ID], states, byID, spec)
		if err != nil {
			return nil, nil, false, err
		}
		switch decision {
		case decisionReady:
			ready = append(ready, node)
		case decisionSkipped:
			skipped = append(skipped, node)
		case decisionTerminal:
			terminal++
		}
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].ID < ready[j].ID })
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].ID < skipped[j].ID })
	return ready, skipped, terminal == len(spec.Nodes), nil
}

// existingNodeDecision 判断已有尝试状态的节点的下一步（ready/terminal/pending
// 或上游失败错误）。
func existingNodeDecision(node domain.Node, state domain.NodeAttempt) (readyDecision, error) {
	switch state.Status {
	case domain.AttemptStatusSucceeded, domain.AttemptStatusSkipped:
		return decisionTerminal, nil
	case domain.AttemptStatusRetryWait:
		if state.RetryAt == nil || !state.RetryAt.After(time.Now()) {
			return decisionReady, nil
		}
		return decisionPending, nil
	case domain.AttemptStatusFailed:
		return decisionFailed, fmt.Errorf("node %s failed", node.ID)
	default:
		return decisionPending, nil
	}
}

// nodeReadyDecision 计算单个节点在当前状态下是否 ready/skipped/terminal/pending，
// 或在入边求值失败时返回错误。逻辑等价于原内联的节点状态机，抽离后 readySet
// 只做分派与聚合。
func nodeReadyDecision(
	node domain.Node,
	state domain.NodeAttempt,
	exists bool,
	edges []domain.Edge,
	states map[string]domain.NodeAttempt,
	byID map[string]domain.Node,
	spec domain.Spec,
) (readyDecision, error) {
	if exists {
		return existingNodeDecision(node, state)
	}
	if len(edges) == 0 {
		return decisionReady, nil
	}
	resolved, selected, selectedSucceeded, err := countIncomingEdgeResolutions(edges, states, byID, spec)
	if err != nil {
		return decisionFailed, err
	}
	if resolved != len(edges) {
		return decisionPending, nil
	}
	if selected == 0 {
		return decisionSkipped, nil
	}
	if selectedSucceeded == selected {
		return decisionReady, nil
	}
	return decisionPending, nil
}

// edgeResolution 描述一条入边的解析结果：是否已终结（resolved）、当前是否被
// 条件路由选中（selected），以及选中时是否成功。
type edgeResolution struct {
	resolved        bool
	selected        bool
	selectedSucceed bool
}

func resolveIncomingEdge(edge domain.Edge, states map[string]domain.NodeAttempt, byID map[string]domain.Node, spec domain.Spec) (edgeResolution, error) {
	source, ok := states[edge.From]
	if !ok {
		return edgeResolution{}, nil
	}
	switch source.Status {
	case domain.AttemptStatusSkipped:
		return edgeResolution{resolved: true}, nil
	case domain.AttemptStatusSucceeded:
		chosen := true
		if byID[edge.From].Type == domain.NodeTypeCondition {
			value, parseErr := strconv.ParseBool(source.OutputSummary)
			if parseErr != nil {
				return edgeResolution{}, parseErr
			}
			chosen = conditionEdgeSelected(spec, edge.From, edge, value)
		}
		return edgeResolution{resolved: true, selected: chosen, selectedSucceed: chosen}, nil
	case domain.AttemptStatusFailed:
		return edgeResolution{}, fmt.Errorf("upstream node %s failed", edge.From)
	}
	return edgeResolution{}, nil
}

func countIncomingEdgeResolutions(edges []domain.Edge, states map[string]domain.NodeAttempt, byID map[string]domain.Node, spec domain.Spec) (resolved, selected, selectedSucceeded int, err error) {
	for _, edge := range edges {
		res, err := resolveIncomingEdge(edge, states, byID, spec)
		if err != nil {
			return 0, 0, 0, err
		}
		if res.resolved {
			resolved++
		}
		if res.selected {
			selected++
			selectedSucceeded++
		}
	}
	return resolved, selected, selectedSucceeded, nil
}

func hasConditionalRouting(spec domain.Spec) bool {
	for _, node := range spec.Nodes {
		if node.Type == domain.NodeTypeCondition {
			return true
		}
	}
	return false
}

// kernelStatus 把节点尝试状态映射为 dag 快照状态，供无条件路由（kernel）路径
// 使用。set 为 false 表示该节点不写入快照（等效 pending）。逻辑等价于原内联
// 的 switch，抽离后 readySetFromKernel 只做装配。
func kernelStatus(node domain.Node, state domain.NodeAttempt, exists bool, now time.Time) (dag.Status, bool, error) {
	if !exists {
		return dag.StatusPending, false, nil
	}
	switch state.Status {
	case domain.AttemptStatusSucceeded, domain.AttemptStatusSkipped:
		return dag.StatusSucceeded, true, nil
	case domain.AttemptStatusFailed:
		return "", false, fmt.Errorf("node %s failed", node.ID)
	case domain.AttemptStatusRetryWait:
		if state.RetryAt != nil && state.RetryAt.After(now) {
			return dag.StatusRunning, true, nil
		}
		return dag.StatusPending, false, nil
	default:
		return dag.StatusRunning, true, nil
	}
}

func readySetFromKernel(
	spec domain.Spec,
	states map[string]domain.NodeAttempt,
	now time.Time,
) (ready, skipped []domain.Node, complete bool, err error) {
	dependencies := make(map[string][]string, len(spec.Nodes))
	for _, edge := range spec.Edges {
		dependencies[edge.To] = append(dependencies[edge.To], edge.From)
	}
	nodes := make([]dag.Node, 0, len(spec.Nodes))
	byID := make(map[string]domain.Node, len(spec.Nodes))
	statuses := make(map[string]dag.Status, len(states))
	for _, node := range spec.Nodes {
		nodes = append(nodes, dag.Node{ID: node.ID, DependsOn: dependencies[node.ID]})
		byID[node.ID] = node
		state, exists := states[node.ID]
		status, set, err := kernelStatus(node, state, exists, now)
		if err != nil {
			return nil, nil, false, err
		}
		if set {
			statuses[node.ID] = status
		}
	}
	readyIDs, _, complete, err := dag.Ready(dag.Snapshot{Nodes: nodes, Statuses: statuses})
	if err != nil {
		return nil, nil, false, err
	}
	ready = make([]domain.Node, 0, len(readyIDs))
	for _, id := range readyIDs {
		ready = append(ready, byID[id])
	}
	return ready, nil, complete, nil
}

func conditionEdgeSelected(spec domain.Spec, sourceID string, edge domain.Edge, value bool) bool {
	if edge.ConditionValue != nil {
		return *edge.ConditionValue == value
	}
	if !edge.Default {
		return false
	}
	for _, candidate := range spec.Edges {
		if candidate.From == sourceID && candidate.ConditionValue != nil && *candidate.ConditionValue == value {
			return false
		}
	}
	return true
}

// buildNodeInputs 构造节点输入映射：无 InputMapping 时按 run_input+nodes 聚合，
// 有则逐字段解析引用。missing 记录字段级引用解析失败的上游节点与原因：不阻断
// 整个节点输入构造，而是返回给调用方触发上游重试（见 retryUpstreamOutput）。
func buildNodeInputs(run *domain.Run, node domain.Node, states map[string]domain.NodeAttempt) (map[string]any, map[string]string) {
	if len(node.InputMapping) == 0 {
		return map[string]any{"run_input": run.Input, "nodes": outputMap(states)}, nil
	}
	mapped := make(map[string]any, len(node.InputMapping))
	missing := make(map[string]string)
	for key, reference := range node.InputMapping {
		value, ok, missingUp, missingReason := resolveMappingReference(reference, run, states)
		if missingUp != "" {
			missing[missingUp] = missingReason
			continue
		}
		if ok {
			mapped[key] = value
		}
	}
	return mapped, missing
}

func nodeInput(run *domain.Run, node domain.Node, states map[string]domain.NodeAttempt) (string, map[string]string, error) {
	incoming := make([]string, 0)
	for _, edge := range run.Snapshot.Edges {
		if edge.To == node.ID {
			if state, ok := states[edge.From]; ok && state.Status == domain.AttemptStatusSucceeded && edgeSelectedByState(run.Snapshot, edge, state) {
				incoming = append(incoming, edge.From)
			}
		}
	}
	if len(incoming) == 0 {
		data, err := json.Marshal(run.Input)
		return string(data), nil, err
	}
	if len(incoming) == 1 && len(node.InputMapping) == 0 {
		return states[incoming[0]].OutputSummary, nil, nil
	}
	inputs, missing := buildNodeInputs(run, node, states)
	data, err := json.Marshal(inputs)
	return string(data), missing, err
}

// resolveMappingReference 解析单条 InputMapping 引用："input" 直传运行输入、
// nodes.<up>.output 透传上游整段输出、nodes.<up>.output.<key> 精确提取字段
// （提取失败返回 missingUp 由调用方触发上游重试），其余未知格式忽略不落映射。
func resolveMappingReference(reference string, run *domain.Run, states map[string]domain.NodeAttempt) (value any, ok bool, missingUp, missingReason string) {
	if reference == "input" {
		return run.Input, true, "", ""
	}
	parts := strings.Split(reference, ".")
	if len(parts) >= 3 && parts[0] == "nodes" && parts[2] == "output" {
		if len(parts) >= 4 {
			// 精确字段引用 nodes.<up>.output.<key>：解析上游 JSON 提取字段。
			value, err := extractOutputField(states[parts[1]].OutputSummary, parts[3])
			if err != nil {
				return nil, false, parts[1], err.Error()
			}
			return value, true, "", ""
		}
		// 整段引用 nodes.<up>.output 保持历史行为：透传上游整段 OutputSummary。
		return states[parts[1]].OutputSummary, true, "", ""
	}
	return nil, false, "", ""
}

// retryUpstreamOutput 把上游输出契约违例（字段引用解析失败）落成对 agent/skill
// 节点的重试：构造 retryable 的 executionOutcome 并复用 commitRetryOrFail 的既有
// 重试通道（下一轮调度器会对过期 retry_wait 重新 ready）。上游不是可契约化的
// agent/skill 节点（作者引用了错误的节点输出）时直接返回错误使 run fail，
// 避免静默拼出错误数据。
func (s *RunService) retryUpstreamOutput(ctx context.Context, tenantID string, run *domain.Run, states map[string]domain.NodeAttempt, upstreamID, reason string) error {
	upstream, ok := nodeByID(run.Snapshot, upstreamID)
	if !ok {
		return fmt.Errorf("workflow contract retry: upstream node %q not found", upstreamID)
	}
	if upstream.Type != domain.NodeTypeAgent && upstream.Type != domain.NodeTypeSkill {
		return fmt.Errorf("workflow contract retry: node %q is not agent or skill, cannot be retried to satisfy output contract: %s", upstreamID, reason)
	}
	state, ok := states[upstreamID]
	if !ok {
		return fmt.Errorf("workflow contract retry: upstream node %q has no completed attempt", upstreamID)
	}
	retryNode := upstream
	if retryNode.Retry.MaxAttempts < contractRetryDefaultAttempts {
		// 内置重试预算仅在作者显式配置低于预算时生效（作者更高配置被尊重）。
		retryNode.Retry.MaxAttempts = contractRetryDefaultAttempts
	}
	outcome := executionOutcome{
		node:    retryNode,
		attempt: state,
		result:  port.NodeExecutionResult{Retryable: true, ErrorCode: "upstream_output_contract"},
		err:     fmt.Errorf("workflow upstream %q output violates contract: %s", upstreamID, reason),
	}
	return s.commitRetryOrFail(ctx, tenantID, outcome)
}

func edgeSelectedByState(spec domain.Spec, edge domain.Edge, state domain.NodeAttempt) bool {
	for _, node := range spec.Nodes {
		if node.ID == edge.From {
			if node.Type != domain.NodeTypeCondition {
				return true
			}
			value, _ := strconv.ParseBool(state.OutputSummary)
			return conditionEdgeSelected(spec, edge.From, edge, value)
		}
	}
	return false
}

func outputMap(states map[string]domain.NodeAttempt) map[string]string {
	out := make(map[string]string)
	for nodeID, state := range states {
		if state.Status == domain.AttemptStatusSucceeded {
			out[nodeID] = state.OutputSummary
		}
	}
	return out
}

func cloneInput(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func terminalOutput(spec domain.Spec, states map[string]domain.NodeAttempt) string {
	outgoing := make(map[string]bool)
	for _, edge := range spec.Edges {
		outgoing[edge.From] = true
	}
	ids := make([]string, 0)
	for _, node := range spec.Nodes {
		if !outgoing[node.ID] {
			ids = append(ids, node.ID)
		}
	}
	sort.Strings(ids)
	outputs := make(map[string]string)
	for _, id := range ids {
		if state, ok := states[id]; ok && state.Status == domain.AttemptStatusSucceeded {
			outputs[id] = state.OutputSummary
		}
	}
	if len(outputs) == 1 {
		for _, output := range outputs {
			return output
		}
	}
	data, _ := json.Marshal(outputs)
	return string(data)
}
