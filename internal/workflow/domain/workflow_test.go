package domain_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/stretchr/testify/require"
)

func linearSpec() domain.Spec {
	return domain.Spec{
		Nodes: []domain.Node{
			{ID: "analyse", Type: domain.NodeTypeAgent, AgentID: "agent-1"},
			{ID: "summarise", Type: domain.NodeTypeAgent, AgentID: "agent-2"},
		},
		Edges: []domain.Edge{{From: "analyse", To: "summarise"}},
	}
}

func TestValidateInputSchemaAcceptsSupportedFieldTypes(t *testing.T) {
	tests := []struct {
		name         string
		fieldType    domain.InputFieldType
		defaultValue any
		options      []domain.InputOption
	}{
		{name: "short text", fieldType: domain.InputFieldShortText, defaultValue: "华东"},
		{name: "long text", fieldType: domain.InputFieldLongText, defaultValue: "详细要求"},
		{name: "number", fieldType: domain.InputFieldNumber, defaultValue: 3.5},
		{name: "single select", fieldType: domain.InputFieldSingleSelect, defaultValue: "east", options: []domain.InputOption{{Label: "华东", Value: "east"}}},
		{name: "multi select", fieldType: domain.InputFieldMultiSelect, defaultValue: []string{"east"}, options: []domain.InputOption{{Label: "华东", Value: "east"}}},
		{name: "boolean", fieldType: domain.InputFieldBoolean, defaultValue: true},
		{name: "date", fieldType: domain.InputFieldDate, defaultValue: "2026-07-23"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := domain.InputSchema{
				TaskLabel: "任务",
				Fields: []domain.InputField{{
					Key: "region", Label: "区域", Type: tt.fieldType,
					Default: tt.defaultValue, Options: tt.options,
				}},
			}
			require.NoError(t, domain.ValidateInputSchema(schema))
		})
	}
}

func TestValidateInputSchemaRejectsInvalidDefinitions(t *testing.T) {
	tooManyFields := make([]domain.InputField, domain.MaxWorkflowInputFields+1)
	for i := range tooManyFields {
		tooManyFields[i] = domain.InputField{Key: fmt.Sprintf("field_%d", i), Label: "字段", Type: domain.InputFieldShortText}
	}
	tests := []struct {
		name   string
		schema domain.InputSchema
	}{
		{name: "missing task label", schema: domain.InputSchema{}},
		{name: "duplicate keys", schema: domain.InputSchema{TaskLabel: "任务", Fields: []domain.InputField{{Key: "region", Label: "区域", Type: domain.InputFieldShortText}, {Key: "region", Label: "市场", Type: domain.InputFieldShortText}}}},
		{name: "reserved task key", schema: domain.InputSchema{TaskLabel: "任务", Fields: []domain.InputField{{Key: "task", Label: "其他任务", Type: domain.InputFieldShortText}}}},
		{name: "missing option value", schema: domain.InputSchema{TaskLabel: "任务", Fields: []domain.InputField{{Key: "region", Label: "区域", Type: domain.InputFieldSingleSelect, Options: []domain.InputOption{{Label: "华东"}}}}}},
		{name: "invalid default", schema: domain.InputSchema{TaskLabel: "任务", Fields: []domain.InputField{{Key: "enabled", Label: "启用", Type: domain.InputFieldBoolean, Default: "yes"}}}},
		{name: "too many fields", schema: domain.InputSchema{TaskLabel: "任务", Fields: tooManyFields}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, domain.ValidateInputSchema(tt.schema), domain.ErrInvalidInputSchema)
		})
	}
}

func TestValidateRunInputAcceptsMixedInput(t *testing.T) {
	schema := domain.InputSchema{TaskLabel: "任务", Fields: []domain.InputField{
		{Key: "count", Label: "数量", Type: domain.InputFieldNumber, Required: true},
		{Key: "region", Label: "区域", Type: domain.InputFieldSingleSelect, Options: []domain.InputOption{{Label: "华东", Value: "east"}}},
		{Key: "channels", Label: "渠道", Type: domain.InputFieldMultiSelect, Options: []domain.InputOption{{Label: "网站", Value: "web"}, {Label: "门店", Value: "store"}}},
		{Key: "enabled", Label: "启用", Type: domain.InputFieldBoolean},
		{Key: "due_date", Label: "日期", Type: domain.InputFieldDate},
	}}
	require.NoError(t, domain.ValidateRunInput(schema, map[string]any{
		"task": "分析市场", "count": float64(3), "region": "east",
		"channels": []any{"web", "store"}, "enabled": false, "due_date": "2026-07-23",
	}))
}

func TestValidateRunInputReturnsFieldIssuesWithoutValues(t *testing.T) {
	schema := domain.InputSchema{TaskLabel: "任务", Fields: []domain.InputField{
		{Key: "count", Label: "数量", Type: domain.InputFieldNumber, Required: true},
		{Key: "region", Label: "区域", Type: domain.InputFieldSingleSelect, Options: []domain.InputOption{{Label: "华东", Value: "east"}}},
		{Key: "channels", Label: "渠道", Type: domain.InputFieldMultiSelect, Options: []domain.InputOption{{Label: "网站", Value: "web"}}},
		{Key: "enabled", Label: "启用", Type: domain.InputFieldBoolean},
	}}
	secret := "sensitive-submitted-value"
	err := domain.ValidateRunInput(schema, map[string]any{
		"task": "", "count": "three", "region": secret,
		"channels": "web", "enabled": "yes", "unknown": secret,
	})
	require.Error(t, err)
	var validationErr *domain.InputValidationError
	require.True(t, errors.As(err, &validationErr))
	require.ElementsMatch(t, []string{"task", "count", "region", "channels", "enabled", "unknown"}, issueFields(validationErr.Issues))
	require.NotContains(t, err.Error(), secret)
}

func issueFields(issues []domain.InputIssue) []string {
	fields := make([]string, 0, len(issues))
	for _, issue := range issues {
		fields = append(fields, issue.Field)
	}
	return fields
}

func TestDefinitionUpdateRequiresExpectedRevision(t *testing.T) {
	def, err := domain.NewDefinition("wf-1", "Research", "desc", linearSpec())
	require.NoError(t, err)
	require.Equal(t, int64(1), def.Revision)

	err = def.UpdateDraft("Research v2", "changed", linearSpec(), 0)
	require.ErrorIs(t, err, domain.ErrRevisionConflict)
	require.Equal(t, int64(1), def.Revision)

	require.NoError(t, def.UpdateDraft("Research v2", "changed", linearSpec(), 1))
	require.Equal(t, int64(2), def.Revision)
}

func TestDefinitionDraftMayBeIncompleteButCannotPublish(t *testing.T) {
	incomplete := domain.Spec{}
	def, err := domain.NewDefinition("wf-1", "Draft", "desc", incomplete)
	require.NoError(t, err)
	require.Error(t, domain.ValidateSpec(def.Spec))
	_, err = def.Publish("version-1", 1)
	require.ErrorIs(t, err, domain.ErrInvalidSpec)

	require.NoError(t, def.UpdateDraft("Draft", "changed", linearSpec(), 1))
	_, err = def.Publish("version-1", 1)
	require.NoError(t, err)
}

func TestValidateSpecRejectsNonLinearOrNonAgentGraphs(t *testing.T) {
	tests := []struct {
		name string
		spec domain.Spec
	}{
		{name: "cycle", spec: domain.Spec{Nodes: []domain.Node{{ID: "a", Type: domain.NodeTypeAgent, AgentID: "a"}, {ID: "b", Type: domain.NodeTypeAgent, AgentID: "b"}}, Edges: []domain.Edge{{From: "a", To: "b"}, {From: "b", To: "a"}}}},
		{name: "unsupported node", spec: domain.Spec{Nodes: []domain.Node{{ID: "skill", Type: "skill"}}}},
		{name: "missing agent", spec: domain.Spec{Nodes: []domain.Node{{ID: "a", Type: domain.NodeTypeAgent}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Error(t, domain.ValidateSpec(tt.spec))
		})
	}
}

func TestValidateSpecAcceptsStaticDiamondAndSupportedNodeTypes(t *testing.T) {
	spec := domain.Spec{
		Nodes: []domain.Node{
			{ID: "start", Type: domain.NodeTypeAgent, AgentID: "agent-1"},
			{ID: "skill", Type: domain.NodeTypeSkill, AgentID: "agent-2", SkillID: "skill-1", SkillRevisionID: "revision-1"},
			{ID: "condition", Type: domain.NodeTypeCondition, Condition: `input.approved == true`},
			{ID: "tool", Type: domain.NodeTypeMCPTool, MCPServerID: "server-1", MCPToolName: "lookup", EffectClass: domain.EffectClassIdempotent},
			{ID: "join", Type: domain.NodeTypeAgent, AgentID: "agent-3"},
		},
		Edges: []domain.Edge{
			{ID: "e1", From: "start", To: "skill"},
			{ID: "e2", From: "start", To: "condition"},
			{ID: "e3", From: "skill", To: "join"},
			{ID: "e4", From: "condition", To: "tool", ConditionValue: boolPtr(true)},
			{ID: "e5", From: "condition", To: "join", Default: true},
			{ID: "e6", From: "tool", To: "join"},
		},
	}
	require.NoError(t, domain.ValidateSpec(spec))
}

func TestValidateSpecCharacterizesEveryNodeType(t *testing.T) {
	tests := []struct {
		name string
		node domain.Node
	}{
		{name: "agent", node: domain.Node{ID: "node", Type: domain.NodeTypeAgent, AgentID: "agent-1"}},
		{
			name: "skill",
			node: domain.Node{
				ID: "node", Type: domain.NodeTypeSkill, AgentID: "agent-1",
				SkillID: "skill-1", SkillRevisionID: "revision-1",
			},
		},
		{
			name: "mcp tool",
			node: domain.Node{
				ID: "node", Type: domain.NodeTypeMCPTool, MCPServerID: "server-1",
				MCPToolName: "lookup", EffectClass: domain.EffectClassPure,
			},
		},
		{name: "approval", node: domain.Node{ID: "node", Type: domain.NodeTypeApproval}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, domain.ValidateSpec(domain.Spec{Nodes: []domain.Node{tt.node}}))
		})
	}

	condition := domain.Spec{
		Nodes: []domain.Node{
			{ID: "condition", Type: domain.NodeTypeCondition, Condition: "input.approved == true"},
			{ID: "next", Type: domain.NodeTypeApproval},
		},
		Edges: []domain.Edge{{From: "condition", To: "next", Default: true}},
	}
	require.NoError(t, domain.ValidateSpec(condition))
}

func TestValidateSpecCharacterizesNodeValidationOrderAndMessages(t *testing.T) {
	tests := []struct {
		name    string
		node    domain.Node
		message string
	}{
		{
			name: "agent identity before policy",
			node: domain.Node{
				ID: "node", Type: domain.NodeTypeAgent, Retry: domain.RetryPolicy{MaxAttempts: -1},
			},
			message: `agent node "node" requires agent_id`,
		},
		{
			name: "skill identity before policy",
			node: domain.Node{
				ID: "node", Type: domain.NodeTypeSkill, Retry: domain.RetryPolicy{MaxAttempts: -1},
			},
			message: `skill node "node" requires agent and pinned revision`,
		},
		{
			name: "mcp identity before policy",
			node: domain.Node{
				ID: "node", Type: domain.NodeTypeMCPTool, Retry: domain.RetryPolicy{MaxAttempts: -1},
			},
			message: `mcp node "node" requires server, tool and effect class`,
		},
		{
			name: "condition expression before policy",
			node: domain.Node{
				ID: "node", Type: domain.NodeTypeCondition, Retry: domain.RetryPolicy{MaxAttempts: -1},
			},
			message: `condition node "node" requires expression`,
		},
		{
			name: "unknown type before policy",
			node: domain.Node{
				ID: "node", Type: "unknown", Retry: domain.RetryPolicy{MaxAttempts: -1},
			},
			message: `unsupported node type "unknown"`,
		},
		{
			name: "policy before output mapping",
			node: domain.Node{
				ID: "node", Type: domain.NodeTypeApproval,
				Retry: domain.RetryPolicy{MaxAttempts: -1}, OutputMapping: map[string]string{"result": "bad"},
			},
			message: "invalid execution policy",
		},
		{
			name: "output mapping after valid policy",
			node: domain.Node{
				ID: "node", Type: domain.NodeTypeApproval, OutputMapping: map[string]string{"result": "bad"},
			},
			message: "invalid output selector",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domain.ValidateSpec(domain.Spec{Nodes: []domain.Node{tt.node}})
			var validationErr *domain.GraphValidationError
			require.ErrorAs(t, err, &validationErr)
			require.Equal(t, tt.message, validationErr.Issues[0].Message)
		})
	}
}

func TestValidateRunInputCharacterizesEveryInputValueType(t *testing.T) {
	options := []domain.InputOption{{Label: "华东", Value: "east"}, {Label: "华南", Value: "south"}}
	tests := []struct {
		name      string
		fieldType domain.InputFieldType
		options   []domain.InputOption
		valid     []any
		invalid   []any
	}{
		{name: "short text", fieldType: domain.InputFieldShortText, valid: []any{"text"}, invalid: []any{1, true}},
		{name: "long text", fieldType: domain.InputFieldLongText, valid: []any{"text"}, invalid: []any{1, true}},
		{
			name: "number", fieldType: domain.InputFieldNumber,
			valid: []any{int8(1), uint64(2), float32(3)}, invalid: []any{"1", true},
		},
		{
			name: "single select", fieldType: domain.InputFieldSingleSelect, options: options,
			valid: []any{"east"}, invalid: []any{"west", []string{"east"}},
		},
		{
			name: "multi select", fieldType: domain.InputFieldMultiSelect, options: options,
			valid:   []any{[]string{"east"}, []any{"east", "south"}},
			invalid: []any{[]string{"west"}, []any{"east", 1}, "east"},
		},
		{name: "boolean", fieldType: domain.InputFieldBoolean, valid: []any{true, false}, invalid: []any{"true", 1}},
		{
			name: "date", fieldType: domain.InputFieldDate, valid: []any{"2024-02-29"},
			invalid: []any{"2023-02-29", "2024-2-29", 20240729},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field := domain.InputField{Key: "value", Label: "值", Type: tt.fieldType, Required: true, Options: tt.options}
			schema := domain.InputSchema{TaskLabel: "任务", Fields: []domain.InputField{field}}
			for _, value := range tt.valid {
				input := map[string]any{"task": "执行", "value": value}
				require.NoError(t, domain.ValidateRunInput(schema, input), "valid value %#v", value)
			}
			for _, value := range tt.invalid {
				err := domain.ValidateRunInput(schema, map[string]any{"task": "执行", "value": value})
				var validationErr *domain.InputValidationError
				require.ErrorAs(t, err, &validationErr, "invalid value %#v", value)
				expected := []domain.InputIssue{{Field: "value", Code: "invalid", Message: "值格式不正确"}}
				require.Equal(t, expected, validationErr.Issues)
			}
		})
	}
}

func TestValidateSpecRejectsUnsafeConditionExpressionAtPublish(t *testing.T) {
	spec := domain.Spec{Nodes: []domain.Node{{ID: "condition", Type: domain.NodeTypeCondition, Condition: `os.exec('rm')`}, {ID: "next", Type: domain.NodeTypeAgent, AgentID: "a"}}, Edges: []domain.Edge{{From: "condition", To: "next", Default: true}}}
	require.ErrorIs(t, domain.ValidateSpec(spec), domain.ErrInvalidSpec)
}

func TestValidateSpecAcceptsMultipleEntriesJoiningOneDAG(t *testing.T) {
	spec := domain.Spec{
		Nodes: []domain.Node{
			{ID: "left", Type: domain.NodeTypeAgent, AgentID: "agent-left"},
			{ID: "right", Type: domain.NodeTypeAgent, AgentID: "agent-right"},
			{ID: "join", Type: domain.NodeTypeAgent, AgentID: "agent-join"},
		},
		Edges: []domain.Edge{{From: "left", To: "join"}, {From: "right", To: "join"}},
	}
	require.NoError(t, domain.ValidateSpec(spec))
}

func TestValidateSpecRejectsLimitsAndInvalidInputReferences(t *testing.T) {
	nodes := make([]domain.Node, domain.MaxWorkflowNodes+1)
	for i := range nodes {
		nodes[i] = domain.Node{ID: fmt.Sprintf("node-%d", i), Type: domain.NodeTypeAgent, AgentID: "agent"}
	}
	require.ErrorIs(t, domain.ValidateSpec(domain.Spec{Nodes: nodes}), domain.ErrInvalidSpec)

	invalidReference := domain.Spec{
		Nodes: []domain.Node{
			{ID: "first", Type: domain.NodeTypeAgent, AgentID: "agent", InputMapping: map[string]string{"bad": "nodes.later.output"}},
			{ID: "later", Type: domain.NodeTypeAgent, AgentID: "agent"},
		},
		Edges: []domain.Edge{{From: "first", To: "later"}},
	}
	require.ErrorIs(t, domain.ValidateSpec(invalidReference), domain.ErrInvalidSpec)
}

func TestValidateSpecRejectsCycleDisconnectedAndConditionWithoutDefault(t *testing.T) {
	tests := []domain.Spec{
		{Nodes: []domain.Node{{ID: "a", Type: domain.NodeTypeAgent, AgentID: "a"}, {ID: "b", Type: domain.NodeTypeAgent, AgentID: "b"}}, Edges: []domain.Edge{{ID: "ab", From: "a", To: "b"}, {ID: "ba", From: "b", To: "a"}}},
		{Nodes: []domain.Node{{ID: "a", Type: domain.NodeTypeAgent, AgentID: "a"}, {ID: "b", Type: domain.NodeTypeAgent, AgentID: "b"}}},
		{Nodes: []domain.Node{{ID: "condition", Type: domain.NodeTypeCondition, Condition: `$.ok == true`}, {ID: "yes", Type: domain.NodeTypeAgent, AgentID: "a"}}, Edges: []domain.Edge{{ID: "yes", From: "condition", To: "yes", ConditionValue: boolPtr(true)}}},
	}
	for _, spec := range tests {
		require.ErrorIs(t, domain.ValidateSpec(spec), domain.ErrInvalidSpec)
	}
}

func TestPausedRunAndAttemptTransitionsAreFenced(t *testing.T) {
	run := domain.Run{ID: "run-1", Status: domain.RunStatusRunning, Generation: 3}
	require.NoError(t, run.Pause("operator"))
	require.Equal(t, int64(4), run.Generation)
	require.Error(t, run.Start())

	attempt := domain.NodeAttempt{Status: domain.AttemptStatusClaimed, FenceToken: 9}
	require.NoError(t, attempt.StartClaimed(9))
	require.ErrorIs(t, attempt.SucceedFenced("late", "trace", 8), domain.ErrFenceConflict)
	require.NoError(t, attempt.SucceedFenced("done", "trace", 9))
}

func boolPtr(value bool) *bool { return &value }

func TestPublishCreatesImmutableSnapshot(t *testing.T) {
	schema := domain.InputSchema{TaskLabel: "任务", Fields: []domain.InputField{{
		Key: "region", Label: "区域", Type: domain.InputFieldShortText,
	}}}
	def, err := domain.NewDefinition("wf-1", "Research", "desc", linearSpec(), schema)
	require.NoError(t, err)
	version, err := def.Publish("version-1", 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), version.Number)

	def.Spec.Nodes[0].AgentID = "changed-agent"
	def.InputSchema.Fields[0].Label = "市场"
	require.Equal(t, "agent-1", version.Spec.Nodes[0].AgentID)
	require.Equal(t, "区域", version.InputSchema.Fields[0].Label)

	run, err := domain.NewRun("run-1", version, map[string]any{"task": "hello", "region": "east"}, "key-1", "hash-1")
	require.NoError(t, err)
	version.Spec.Nodes[0].AgentID = "another-agent"
	require.Equal(t, "agent-1", run.Snapshot.Nodes[0].AgentID)
}

func TestRunAndAttemptTransitionsAreFailClosed(t *testing.T) {
	run := domain.Run{ID: "run-1", Status: domain.RunStatusQueued}
	require.NoError(t, run.Start())
	require.Error(t, run.Complete(""))
	require.NoError(t, run.Fail("node failed"))
	require.Error(t, run.Start())

	attempt := domain.NodeAttempt{Status: domain.AttemptStatusPending}
	require.NoError(t, attempt.Start())
	require.NoError(t, attempt.Succeed("answer", "trace-1"))
	require.Error(t, attempt.Fail("late failure"))
}

func TestRunPersistentControlsAndAvailableActions(t *testing.T) {
	run := &domain.Run{ID: "run-1", Status: domain.RunStatusRunning, Generation: 4}
	require.NoError(t, run.RequestPause("operator maintenance", 4))
	require.Equal(t, domain.RunStatusPauseRequested, run.Status)
	require.Equal(t, int64(5), run.Generation)
	require.NoError(t, run.MarkPaused(5))
	require.NoError(t, run.Resume(5))
	require.Equal(t, domain.RunStatusQueued, run.Status)
	require.Equal(t, int64(6), run.Generation)
	require.NoError(t, run.RequestCancel(6))
	require.NoError(t, run.MarkCanceled(7))
	require.Empty(t, run.AvailableActions(false, false))
}

func TestRunControlRejectsStaleGenerationAndPendingApprovalResume(t *testing.T) {
	run := &domain.Run{ID: "run-1", Status: domain.RunStatusPaused, Generation: 8, PauseReason: "approval required"}
	require.ErrorIs(t, run.Resume(7), domain.ErrGenerationConflict)
	require.False(t, slices.Contains(run.AvailableActions(true, false), "resume"))
}

func TestApprovalDecisionIsFencedAndSingleUse(t *testing.T) {
	approval := domain.NewApproval("approval-1", "run-1", "node-1", "attempt-1", 3, "high risk", "high", "redacted")
	require.ErrorIs(t, approval.Decide(domain.ApprovalDecisionApprove, "admin-1", "ok", 2, "attempt-1"), domain.ErrGenerationConflict)
	require.NoError(t, approval.Decide(domain.ApprovalDecisionApprove, "admin-1", "ok", 3, "attempt-1"))
	require.ErrorIs(t, approval.Decide(domain.ApprovalDecisionReject, "admin-2", "no", 3, "attempt-1"), domain.ErrDecisionConflict)
}

func TestEffectIntentUnknownRequiresManualIntervention(t *testing.T) {
	intent := domain.NewEffectIntent("effect-1", "run-1", "node-1", "attempt-1", 5, domain.EffectClassNonIdempotent, "stable-key")
	require.NoError(t, intent.Start(5))
	require.NoError(t, intent.MarkUnknown("worker lost", 5))
	require.Equal(t, domain.EffectIntentStatusUnknown, intent.Status)
	require.True(t, intent.RequiresManualIntervention())
}

func TestValidateSpecRejectsUnknownEffectClass(t *testing.T) {
	spec := domain.Spec{Nodes: []domain.Node{{ID: "tool", Type: domain.NodeTypeMCPTool, MCPServerID: "crm", MCPToolName: "write", EffectClass: "garbage"}}}
	require.ErrorIs(t, domain.ValidateSpec(spec), domain.ErrInvalidSpec)
}

func TestValidateSpecRejectsConditionReferencesMissingOrNonUpstreamNode(t *testing.T) {
	for _, expression := range []string{"nodes.missing.output == 'yes'", "nodes.after.output == 'yes'"} {
		spec := domain.Spec{Nodes: []domain.Node{{ID: "condition", Type: domain.NodeTypeCondition, Condition: expression}, {ID: "after", Type: domain.NodeTypeAgent, AgentID: "a"}}, Edges: []domain.Edge{{From: "condition", To: "after", Default: true}}}
		require.ErrorIs(t, domain.ValidateSpec(spec), domain.ErrInvalidSpec)
	}
}

func TestApprovalRejectsUnknownDecision(t *testing.T) {
	approval := domain.NewApproval("approval", "run", "node", "attempt", 1, "risk", "high", "safe")
	require.ErrorIs(t, approval.Decide("approve ", "admin", "", 1, "attempt"), domain.ErrInvalidTransition)
	require.Equal(t, domain.ApprovalStatusPending, approval.Status)
}

func TestNodePositionRoundTripsThroughJSON(t *testing.T) {
	// 与 pgx 写 JSONB 相同路径：Marshal → string → Unmarshal。
	spec := domain.Spec{Nodes: []domain.Node{{
		ID: "agent-1", Type: domain.NodeTypeAgent, AgentID: "a",
		Position: &domain.NodePosition{X: 120.5, Y: -40},
	}}}
	encoded, err := json.Marshal(spec)
	require.NoError(t, err)
	var decoded domain.Spec
	require.NoError(t, json.Unmarshal([]byte(string(encoded)), &decoded))
	require.NotNil(t, decoded.Nodes[0].Position)
	require.Equal(t, 120.5, decoded.Nodes[0].Position.X)
	require.Equal(t, -40.0, decoded.Nodes[0].Position.Y)
	require.NoError(t, domain.ValidateSpec(decoded))
}

func TestNodePositionIsOptionalForLegacySpecs(t *testing.T) {
	// 历史 spec 无 position 字段：可解析、nil、JSON 不输出、校验通过。
	legacy := `{"nodes":[{"id":"agent-1","type":"agent","agent_id":"a"}],"edges":[]}`
	var decoded domain.Spec
	require.NoError(t, json.Unmarshal([]byte(legacy), &decoded))
	require.Nil(t, decoded.Nodes[0].Position)
	require.NoError(t, domain.ValidateSpec(decoded))
	encoded, err := json.Marshal(decoded)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "position")
}

func TestCloneSpecDeepCopiesPosition(t *testing.T) {
	def, err := domain.NewDefinition("wf-1", "Research", "desc", domain.Spec{Nodes: []domain.Node{{
		ID: "agent-1", Type: domain.NodeTypeAgent, AgentID: "a", Position: &domain.NodePosition{X: 10, Y: 20},
	}}}, domain.InputSchema{TaskLabel: "任务"})
	require.NoError(t, err)
	version, err := def.Publish("version-1", 1)
	require.NoError(t, err)

	def.Spec.Nodes[0].Position.X = 999
	require.Equal(t, 10.0, version.Spec.Nodes[0].Position.X)
}

func TestValidateNodeRejectsNonFiniteOrHugePosition(t *testing.T) {
	// NaN/Inf 无法经 JSON 到达（encoding/json 直接报错），直接构造验证领域不变量。
	tests := []struct {
		name     string
		position domain.NodePosition
	}{
		{name: "NaN x", position: domain.NodePosition{X: math.NaN(), Y: 0}},
		{name: "Inf y", position: domain.NodePosition{X: 0, Y: math.Inf(1)}},
		{name: "beyond limit", position: domain.NodePosition{X: 1e7, Y: 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := domain.Spec{Nodes: []domain.Node{{
				ID: "agent-1", Type: domain.NodeTypeAgent, AgentID: "a", Position: &tc.position,
			}}}
			require.ErrorIs(t, domain.ValidateSpec(spec), domain.ErrInvalidSpec)
		})
	}
}

func TestValidateSpecGraphAllowsEmptyAndIncompleteDrafts(t *testing.T) {
	// 草稿校验容忍空图（画一半先保存）与字段未填全的半成品节点。
	require.NoError(t, domain.ValidateSpecGraph(domain.Spec{}))

	// condition 节点要求恰好一条 default 边（图完整性规则），单 condition 节点会被拒，
	// 这里只验证不违反图完整性的半成品：mcp_tool 缺 server/tool、agent 缺 agent_id。
	for _, incomplete := range []domain.Spec{
		{Nodes: []domain.Node{{ID: "mcp", Type: domain.NodeTypeMCPTool}}},
		{Nodes: []domain.Node{{ID: "agent", Type: domain.NodeTypeAgent, AgentID: ""}}},
	} {
		require.NoError(t, domain.ValidateSpecGraph(incomplete))
	}
}

func TestValidateSpecGraphRejectsCycleAndDisconnected(t *testing.T) {
	cycle := domain.Spec{Nodes: []domain.Node{
		{ID: "a", Type: domain.NodeTypeAgent, AgentID: "a"},
		{ID: "b", Type: domain.NodeTypeAgent, AgentID: "b"},
	}, Edges: []domain.Edge{{From: "a", To: "b"}, {From: "b", To: "a"}}}
	require.ErrorIs(t, domain.ValidateSpecGraph(cycle), domain.ErrInvalidSpec)

	disconnected := domain.Spec{Nodes: []domain.Node{
		{ID: "a", Type: domain.NodeTypeAgent, AgentID: "a"},
		{ID: "b", Type: domain.NodeTypeAgent, AgentID: "b"},
	}}
	require.ErrorIs(t, domain.ValidateSpecGraph(disconnected), domain.ErrInvalidSpec)
}

func TestValidateSkillBinding(t *testing.T) {
	// 空 allowedSkills 表示 agent 未启用任何技能，引用任何 skill 都被拒绝。
	require.ErrorIs(t, domain.ValidateSkillBinding(nil, "skill-1"), domain.ErrInvalidSpec)
	require.ErrorIs(t, domain.ValidateSkillBinding([]string{}, "skill-1"), domain.ErrInvalidSpec)
	// 不在允许列表内拒绝。
	require.ErrorIs(t, domain.ValidateSkillBinding([]string{"skill-2"}, "skill-1"), domain.ErrInvalidSpec)
	// 命中允许列表通过。
	require.NoError(t, domain.ValidateSkillBinding([]string{"skill-1", "skill-2"}, "skill-2"))
}
