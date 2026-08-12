package wiring

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/byteBuilderX/stratum/api/http/dto/gen"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	evalapp "github.com/byteBuilderX/stratum/internal/evaluation/application"
	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	evalport "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	mcpapp "github.com/byteBuilderX/stratum/internal/mcp/application"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"go.uber.org/zap"
)

// approvalActionExecutor 把审批通过后的动作分发到对应 context 的 service
// （wiring 薄 ACL，D3/D4/D5）。执行路径与 admin/owner 直接执行完全一致。
type approvalActionExecutor struct {
	suiteSvc        *evalapp.SuiteService
	jobSvc          *evalapp.JobService
	baselineSvc     *evalapp.BaselineService
	experimentSvc   *evalapp.ExperimentService
	optimizationSvc *evalapp.OptimizationService
	casegen         *evalapp.TestCaseGenerator
	candidateSvc    *evalapp.CandidateCommandService
	agentApplier    evalport.AgentRevisionApplier
	mcpSvc          *mcpapp.MCPService
	logger          *zap.Logger
}

// newApprovalActionExecutor 从已装配的 Evaluation / MCP 组件收集 service。
// mcpComp 可为 nil（MCP 组件未装配时 executeMCPConfig fail closed）。
func newApprovalActionExecutor(evalComp *Evaluation, mcpComp *MCP, logger *zap.Logger) *approvalActionExecutor {
	var mcpSvc *mcpapp.MCPService
	if mcpComp != nil {
		mcpSvc = mcpComp.Service
	}
	return &approvalActionExecutor{
		suiteSvc:        evalComp.SuiteService,
		jobSvc:          evalComp.JobService,
		baselineSvc:     evalComp.BaselineService,
		experimentSvc:   evalComp.ExperimentService,
		optimizationSvc: evalComp.OptimizationService,
		casegen:         evalComp.TestCaseGenerator,
		candidateSvc:    evalComp.CandidateService,
		agentApplier:    evalComp.AgentRevisionApplier,
		mcpSvc:          mcpSvc,
		logger:          logger,
	}
}

// ExecuteApprovalAction 按 subject kind 分发到对应 context 的执行器。
func (e *approvalActionExecutor) ExecuteApprovalAction(
	ctx context.Context, req port.ApprovalActionRequest,
) (map[string]any, error) {
	switch req.SubjectKind {
	case agentdomain.SubjectKindEvaluationAction:
		return e.executeEvaluation(ctx, req)
	case agentdomain.SubjectKindMCPPolicy, agentdomain.SubjectKindMCPServer:
		return e.executeMCPConfig(ctx, req)
	default:
		return nil, notExecuted(fmt.Errorf("unsupported approval subject kind: %s", req.SubjectKind))
	}
}

// evaluationActionFunc 执行一个评测 operation（D4 审批动作）。
type evaluationActionFunc func(ctx context.Context, e *approvalActionExecutor, req port.ApprovalActionRequest) (map[string]any, error)

// evaluationOperations 把 operation 分派到独立方法——避免单一巨型 switch 突破
// 圈复杂度/认知复杂度/行数门禁（各方法 CC≤2）。
var evaluationOperations = map[string]evaluationActionFunc{
	"create_suite":          executeCreateSuite,
	"publish_suite":         executePublishSuite,
	"generate_suite_cases":  executeGenerateSuiteCases,
	"enqueue_run":           executeEnqueueRun,
	"create_experiment":     executeCreateExperiment,
	"pause_experiment":      executePauseExperiment,
	"promote_experiment":    executePromoteExperiment,
	"rollback_experiment":   executeRollbackExperiment,
	"reject_candidate":      executeRejectCandidate,
	"create_baseline":       executeCreateBaseline,
	"generate_optimization": executeGenerateOptimization,
}

// executeEvaluation 按 operation 分发评测原方法，与 admin/owner 直接执行同一代码路径。
func (e *approvalActionExecutor) executeEvaluation(
	ctx context.Context, req port.ApprovalActionRequest,
) (map[string]any, error) {
	if e.suiteSvc == nil || e.jobSvc == nil || e.baselineSvc == nil ||
		e.experimentSvc == nil || e.optimizationSvc == nil || e.casegen == nil || e.candidateSvc == nil {
		return nil, notExecuted(fmt.Errorf("evaluation approval executor not fully configured"))
	}
	operation, _ := req.Arguments["operation"].(string)
	fn, ok := evaluationOperations[operation]
	if !ok {
		return nil, notExecuted(fmt.Errorf("unsupported evaluation operation: %s", operation))
	}
	return fn(ctx, e, req)
}

// notExecuted 包装预执行失败（评测 service 写操作事务性：返回错误 = 无副作用），
// 审批释放回 approved 可重试，而非烧成终态 unknown_outcome。
func notExecuted(err error) error {
	return &port.ApprovalActionNotExecutedError{Err: err}
}

func executeCreateSuite(ctx context.Context, e *approvalActionExecutor, req port.ApprovalActionRequest) (map[string]any, error) {
	suite, revision, err := e.suiteSvc.Create(ctx, req.TenantID, evalapp.CreateSuiteInput{
		Name:         asString(req.Arguments, "name"),
		Description:  asString(req.Arguments, "description"),
		ResourceKind: evaldomain.ResourceKind(asString(req.Arguments, "resource_kind")),
		Cases:        asEvalCases(req.Arguments, "cases"),
	})
	if err != nil {
		return nil, notExecuted(err)
	}
	return map[string]any{"suite_id": suite.ID, "revision_id": revision.ID}, nil
}

func executePublishSuite(ctx context.Context, e *approvalActionExecutor, req port.ApprovalActionRequest) (map[string]any, error) {
	revision, err := e.suiteSvc.Publish(ctx, req.TenantID, asString(req.Arguments, "suiteID"))
	if err != nil {
		return nil, notExecuted(err)
	}
	return map[string]any{"revision_id": revision.ID}, nil
}

// executeGenerateSuiteCases 与直接路径同输入（直接 handler 不设 RequestedBy，评审
// 一致化要求执行器也不设——两者保持完全一致）。
func executeGenerateSuiteCases(ctx context.Context, e *approvalActionExecutor, req port.ApprovalActionRequest) (map[string]any, error) {
	result, err := e.casegen.Generate(ctx, evalapp.GenerateInput{
		TenantID: req.TenantID,
		SuiteID:  asString(req.Arguments, "suiteID"),
		Policy:   evaldomain.SamplePolicy(asString(req.Arguments, "samplePolicy")),
		MaxCases: asInt(req.Arguments, "maxCases"),
	})
	if err != nil {
		return nil, notExecuted(err)
	}
	return map[string]any{"samples_found": result.SamplesFound, "generated": result.Generated,
		"rejected": result.Rejected}, nil
}

// executeEnqueueRun 发起人记为审批请求的 ActorID（enqueue 是成员发起动作）。
func executeEnqueueRun(ctx context.Context, e *approvalActionExecutor, req port.ApprovalActionRequest) (map[string]any, error) {
	job, err := e.jobSvc.EnqueueRun(ctx, req.TenantID, evalapp.EnqueueRunInput{
		Resource:        asResourceRef(req.Arguments, "resource"),
		SuiteRevisionID: asString(req.Arguments, "suiteRevisionID"),
		IdempotencyKey:  asString(req.Arguments, "idempotencyKey"),
		RequestedBy:     req.ActorID,
	})
	if err != nil {
		return nil, notExecuted(err)
	}
	return map[string]any{"job_id": job.ID}, nil
}

func executeCreateExperiment(ctx context.Context, e *approvalActionExecutor, req port.ApprovalActionRequest) (map[string]any, error) {
	experiment, _, err := e.experimentSvc.Create(ctx, req.TenantID, evalapp.CreateExperimentInput{
		Stable:          asResourceRef(req.Arguments, "stable"),
		Canary:          asResourceRef(req.Arguments, "canary"),
		SuiteRevisionID: asString(req.Arguments, "suiteRevisionID"),
	})
	if err != nil {
		return nil, notExecuted(err)
	}
	return map[string]any{"experiment_id": experiment.ID}, nil
}

func executePauseExperiment(ctx context.Context, e *approvalActionExecutor, req port.ApprovalActionRequest) (map[string]any, error) {
	if _, err := e.experimentSvc.Pause(ctx, req.TenantID, asString(req.Arguments, "experimentID"),
		asExperimentCommand(req, "reason")); err != nil {
		return nil, notExecuted(err)
	}
	return map[string]any{"status": "paused"}, nil
}

func executePromoteExperiment(ctx context.Context, e *approvalActionExecutor, req port.ApprovalActionRequest) (map[string]any, error) {
	result, err := e.experimentSvc.Promote(ctx, req.TenantID, asString(req.Arguments, "experimentID"),
		asExperimentCommand(req, "reason"))
	if err != nil {
		return nil, notExecuted(err)
	}
	// 与 handler 直接路径一致：Agent 资源写回生产 agents 表。
	e.applyPromotedAgent(ctx, req, result)
	return map[string]any{"status": "promoted", "experiment_id": result.ID}, nil
}

// applyPromotedAgent 评测 promote 成功后把 Agent 资源写回生产 agents 表。
// 与直接路径相同：写回失败仅告警，不失败动作（动作已产生副作用）。
func (e *approvalActionExecutor) applyPromotedAgent(ctx context.Context, req port.ApprovalActionRequest, result evaldomain.Experiment) {
	if result.ResourceKind != evaldomain.ResourceKindAgent || e.agentApplier == nil {
		return
	}
	if applyErr := e.agentApplier.ApplyPublishedRevision(
		ctx, req.TenantID, result.ResourceID, result.CanaryRevisionID,
	); applyErr != nil && e.logger != nil {
		e.logger.Warn("approval execute promote: agent write-back failed",
			zap.String("agent_id", result.ResourceID),
			zap.String("revision_id", result.CanaryRevisionID),
			zap.Error(applyErr),
		)
	}
}

func executeRollbackExperiment(ctx context.Context, e *approvalActionExecutor, req port.ApprovalActionRequest) (map[string]any, error) {
	if _, err := e.experimentSvc.Rollback(ctx, req.TenantID, asString(req.Arguments, "experimentID"),
		asExperimentCommand(req, "reason")); err != nil {
		return nil, notExecuted(err)
	}
	return map[string]any{"status": "rolled_back"}, nil
}

// executeRejectCandidate 执行者记为审批人（DecidedBy）：拒绝动作代表审批人意志。
func executeRejectCandidate(ctx context.Context, e *approvalActionExecutor, req port.ApprovalActionRequest) (map[string]any, error) {
	if _, err := e.candidateSvc.Reject(ctx, req.TenantID, asString(req.Arguments, "candidateID"),
		evalapp.CandidateCommandInput{
			ActorID: req.DecidedBy, Reason: asString(req.Arguments, "reason"),
			IdempotencyKey:       asString(req.Arguments, "idempotencyKey"),
			ExpectedStateVersion: int64(asInt(req.Arguments, "expectedStateVersion")),
		}); err != nil {
		return nil, notExecuted(err)
	}
	return map[string]any{"status": "rejected"}, nil
}

func executeCreateBaseline(ctx context.Context, e *approvalActionExecutor, req port.ApprovalActionRequest) (map[string]any, error) {
	ref, err := e.baselineSvc.CreatePublishedBaseline(
		ctx, req.TenantID, evaldomain.ResourceKind(asString(req.Arguments, "resourceKind")),
		asString(req.Arguments, "resourceID"),
	)
	if err != nil {
		return nil, notExecuted(err)
	}
	return map[string]any{"kind": ref.Kind, "resource_id": ref.ResourceID, "revision_id": ref.RevisionID}, nil
}

func executeGenerateOptimization(ctx context.Context, e *approvalActionExecutor, req port.ApprovalActionRequest) (map[string]any, error) {
	job, candidates, err := e.optimizationSvc.Generate(ctx, req.TenantID, evalapp.GenerateCandidatesInput{
		IdempotencyKey:   asString(req.Arguments, "idempotencyKey"),
		Baseline:         asResourceRef(req.Arguments, "baseline"),
		SuiteRevisionID:  asString(req.Arguments, "suiteRevisionID"),
		SearchSpace:      asSearchSpace(req.Arguments, "searchSpace"),
		FailureSummaries: asStringSlice(req.Arguments, "failureSummaries"),
	})
	if err != nil {
		return nil, notExecuted(err)
	}
	return map[string]any{"optimization_id": job.ID, "candidate_count": len(candidates)}, nil
}

// mcpConfigActionFunc 执行一个 MCP 配置 operation（D5 审批动作）。
type mcpConfigActionFunc func(ctx context.Context, e *approvalActionExecutor, req port.ApprovalActionRequest) (map[string]any, error)

// mcpConfigOperations 把 operation 分派到独立方法——避免单一巨型 switch 突破
// 圈复杂度/认知复杂度门禁（各方法 CC≤2）。
var mcpConfigOperations = map[string]mcpConfigActionFunc{
	"set_tool_policy": executeMCPSetToolPolicy,
	"connect_server":  executeMCPConnectServer,
	"update_server":   executeMCPUpdateServer,
	"set_editors":     executeMCPSetEditors,
	"delete_server":   executeMCPDeleteServer,
}

// executeMCPConfig 按 operation 分发 MCP 原方法（D5）。actor 一律用 req.DecidedBy——
// 审批人以自身权限执行：member 发起 connect 审批通过后 ownership 校验能过（DecidedBy 是
// admin/owner）；未知 operation fail closed（notExecuted → 审批释放回 approved 可重试）。
// 错误语义与 evaluation 的有意分歧：MCP 写方法非事务性，重试可能重复应用配置
// （double-connect/覆盖），故业务错误原样返回 → 终态 unknown_outcome（不可重试），
// 而非 notExecuted 释放回 approved；仅预执行失败（mcpSvc 未装配、未知 operation、
// config 解码失败）用 notExecuted 保持可重试。
func (e *approvalActionExecutor) executeMCPConfig(
	ctx context.Context, req port.ApprovalActionRequest,
) (map[string]any, error) {
	if e.mcpSvc == nil {
		return nil, notExecuted(fmt.Errorf("mcp approval executor not configured"))
	}
	operation := asString(req.Arguments, "operation")
	fn, ok := mcpConfigOperations[operation]
	if !ok {
		return nil, notExecuted(fmt.Errorf("unsupported mcp operation: %s", operation))
	}
	return fn(ctx, e, req)
}

func executeMCPSetToolPolicy(ctx context.Context, e *approvalActionExecutor, req port.ApprovalActionRequest) (map[string]any, error) {
	if err := e.mcpSvc.SetToolPolicy(ctx, mcpdomain.ToolPolicy{
		ServerID: asString(req.Arguments, "serverId"), ToolName: asString(req.Arguments, "toolName"),
		RiskLevel: mcpdomain.ToolRiskLevel(asString(req.Arguments, "riskLevel")), UpdatedBy: req.DecidedBy,
	}); err != nil {
		return nil, err
	}
	return map[string]any{"status": "updated"}, nil
}

func executeMCPConnectServer(ctx context.Context, e *approvalActionExecutor, req port.ApprovalActionRequest) (map[string]any, error) {
	cfg, err := decodeServerConfig(req.Arguments)
	if err != nil {
		return nil, notExecuted(err)
	}
	// 与直接路径一致：连接是活网络操作，可越过审批人的请求生命周期。沿用可取消的
	// request ctx 会在审批人断开时取消连接——业务失败 → unknown_outcome 烧审批。
	if err := e.mcpSvc.ConnectServer(context.WithoutCancel(ctx), cfg, asStringSlice(req.Arguments, "editors"), req.DecidedBy); err != nil {
		return nil, err
	}
	return map[string]any{"server_id": cfg.ID}, nil
}

func executeMCPUpdateServer(ctx context.Context, e *approvalActionExecutor, req port.ApprovalActionRequest) (map[string]any, error) {
	cfg, err := decodeServerConfig(req.Arguments)
	if err != nil {
		return nil, notExecuted(err)
	}
	cfg.ID = asString(req.Arguments, "serverId")
	if err := e.mcpSvc.UpdateServer(ctx, cfg, req.DecidedBy); err != nil {
		return nil, err
	}
	return map[string]any{"server_id": cfg.ID}, nil
}

func executeMCPSetEditors(ctx context.Context, e *approvalActionExecutor, req port.ApprovalActionRequest) (map[string]any, error) {
	if err := e.mcpSvc.SetEditors(ctx, asString(req.Arguments, "serverId"), req.DecidedBy, asStringSlice(req.Arguments, "editorIds")); err != nil {
		return nil, err
	}
	return map[string]any{"status": "updated"}, nil
}

func executeMCPDeleteServer(ctx context.Context, e *approvalActionExecutor, req port.ApprovalActionRequest) (map[string]any, error) {
	if err := e.mcpSvc.DeleteServer(ctx, asString(req.Arguments, "serverId"), req.DecidedBy); err != nil {
		return nil, err
	}
	return map[string]any{"status": "deleted"}, nil
}

// decodeServerConfig 从审批 args 反序列化 MCP 服务器配置（存储后形态 map[string]any，
// 经 gen 请求结构还原为 domain.ServerConfig；system_key 由 ServerConfig 校验拒绝）。
func decodeServerConfig(args map[string]any) (*mcpdomain.ServerConfig, error) {
	raw, err := json.Marshal(args["config"])
	if err != nil {
		return nil, fmt.Errorf("encode mcp config arg: %w", err)
	}
	var req gen.MCPServerConfigRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("decode mcp config arg: %w", err)
	}
	// handler bind 的 DTO 里 SystemKey 无 omitempty：成员未提供 system_key 时，经 AES
	// round-trip 后空值落成字面 "null"，ServerConfig() 会误判为系统托管 key 而拒绝——
	// connect/update 审批永远 notExecuted 释放回 approved 死循环（评审 BLOCKING）。
	// 非空 system_key 已在 handler bind 层双路径（member/admin）拒绝，此处归一化安全。
	if len(req.SystemKey) == 0 || string(bytes.TrimSpace(req.SystemKey)) == "null" {
		req.SystemKey = nil
	}
	return req.ServerConfig()
}

// asExperimentCommand 组装实验命令，执行者记为审批人（DecidedBy）。
func asExperimentCommand(req port.ApprovalActionRequest, reasonKey string) evalapp.ExperimentCommandInput {
	return evalapp.ExperimentCommandInput{
		ActorID: req.DecidedBy, Reason: asString(req.Arguments, reasonKey),
		IdempotencyKey:       asString(req.Arguments, "idempotencyKey"),
		ExpectedStateVersion: int64(asInt(req.Arguments, "expectedStateVersion")),
	}
}

func asString(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}

func asInt(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

// asStringSlice 缺失/空 key 返回 nil，保持与直接路径一致：OptimizationFingerprint
// JSON 序列化 nil 为 null、空 slice 为 []，两者 SHA 不同，跨路径会生成不同幂等键。
// 兼容两种形态：JSON 持久化后 []any 与内存直传 []string。
func asStringSlice(args map[string]any, key string) []string {
	switch raw := args[key].(type) {
	case []any:
		if raw == nil {
			return nil
		}
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return raw
	default:
		return nil
	}
}

// asResourceRef 从 {"kind","id","revision_id"} 映射到 eval domain 的 ResourceRef。
func asResourceRef(args map[string]any, key string) evaldomain.ResourceRef {
	m, _ := args[key].(map[string]any)
	return evaldomain.ResourceRef{
		Kind:       evaldomain.ResourceKind(asString(m, "kind")),
		ResourceID: asString(m, "id"),
		RevisionID: asString(m, "revision_id"),
	}
}

// asEvalCases 从 JSON 持久化后的 []any[map[string]any] 还原评测用例。
func asEvalCases(args map[string]any, key string) []evaldomain.EvalCase {
	raw, _ := args[key].([]any)
	out := make([]evaldomain.EvalCase, 0, len(raw))
	for _, item := range raw {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		out = append(out, evaldomain.EvalCase{
			Name: asString(m, "name"), Input: m["input"], ExpectedOutput: m["expected_output"],
			AssertionMode: evaldomain.AssertionMode(asString(m, "assertion_mode")),
			Enabled:       asBool(m, "enabled"),
		})
	}
	return out
}

// asSearchSpace 兼容 JSONB 反序列化（map[string]any + []any）与内存直传
// （map[string][]any）两种形态。缺失 key 返回 nil，保持与直接路径一致：
// OptimizationFingerprint 序列化 nil 为 null、空 map 为 {}，两者 SHA 不同。
func asSearchSpace(args map[string]any, key string) map[string][]any {
	switch v := args[key].(type) {
	case map[string][]any:
		return v
	case map[string]any:
		out := map[string][]any{}
		for k, item := range v {
			switch items := item.(type) {
			case []any:
				out[k] = items
			case []string:
				conv := make([]any, 0, len(items))
				for _, s := range items {
					conv = append(conv, s)
				}
				out[k] = conv
			}
		}
		return out
	default:
		return nil
	}
}

func asBool(args map[string]any, key string) bool {
	b, _ := args[key].(bool)
	return b
}
