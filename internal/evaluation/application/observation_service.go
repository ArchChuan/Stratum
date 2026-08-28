package application

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// 运行态观测 judge rubric 维度（规格 §3.1）：三个语义质量维度各一次 Judge 调用。
var observationJudgeDimensions = []string{"faithfulness", "relevance", "completeness"}

// ObservationServiceDeps 是落地服务的依赖（全部必填，缺失字段由 wiring 保证；
// 任何依赖 nil 时 Process 按 fail closed 处理）。
type ObservationServiceDeps struct {
	Enabled    func(ctx context.Context) bool    // 平台参数 evaluation.observe.enabled
	SampleRate func(ctx context.Context) float64 // 平台参数 evaluation.observe.sample_rate
	Evidence   port.TraceEvidenceReader
	Judge      port.LLMJudge
	Repo       port.ObservationRepository
	Metrics    observability.MetricsProvider
	Logger     *zap.Logger
}

type ObservationService struct {
	deps ObservationServiceDeps
}

func NewObservationService(deps ObservationServiceDeps) *ObservationService {
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &ObservationService{deps: deps}
}

// Process 处理一条观测引用事件：开启 → 采样 → judge 可用性 → 拉证据 → judge 多维打分 → 落库。
// 返回 error 表示需要 NATS 重投（仅证据查询失败）；judge 关闭 / judge 故障 / 校验非法 /
// 落库失败均在本服务内丢弃并返回 nil（§14 精神：不落零信号 pass 观测、不制造 poison
// message 重投循环），绝不伪成功。
func (s *ObservationService) Process(ctx context.Context, evt domain.ObservationReferenceEvent) error {
	if !s.deps.Enabled(ctx) {
		return nil
	}
	if !sampleDecision(s.deps.SampleRate(ctx), evt.ResourceKind, evt.TraceID) {
		return nil
	}
	// judge 关闭（nil 或 !Enabled）时跳过本次观测：观测的产出是 judge 信号，
	// 无 judge 不落零信号 pass 观测（§14 精神）。配置态跳过，非故障降级、不计数。
	if s.deps.Judge == nil || !s.deps.Judge.Enabled(ctx) {
		return nil
	}
	trace, err := s.deps.Evidence.Resolve(ctx, evt.TenantID, evt.TraceID)
	if err != nil {
		s.deps.Metrics.IncEvalJudgeFailure("evidence_resolve")
		return fmt.Errorf("observation resolve evidence: %w", err)
	}
	obs := s.buildObservation(evt, trace)
	if err := s.applyJudge(ctx, trace, &obs); err != nil {
		// judge 不可用：§14 采样降级跳过——不落零信号的伪 pass 观察、不重投
		// （避免 judge 持续不可用时的重投空转），仅指标计数 + warn 日志。
		s.deps.Logger.Warn("observation judge degraded, skip", zap.Error(err),
			zap.String("trace_id", evt.TraceID))
		s.deps.Metrics.IncEvalJudgeFailure("judge_unavailable")
		return nil
	}
	if err := obs.Validate(); err != nil {
		// 数据非法：重投必再失败（poison message 循环），丢弃而非重投。
		s.deps.Logger.Warn("observation invalid, drop", zap.Error(err),
			zap.String("trace_id", evt.TraceID))
		s.deps.Metrics.IncEvalJudgeFailure("invalid_observation")
		return nil
	}
	if err := s.deps.Repo.Save(ctx, evt.TenantID, &obs); err != nil {
		// 重投会因每次 buildObservation 新 uuid 重复落库，丢弃而非重投。
		s.deps.Logger.Warn("observation save failed, drop", zap.Error(err),
			zap.String("trace_id", evt.TraceID))
		s.deps.Metrics.IncEvalJudgeFailure("save_failed")
		return nil
	}
	s.deps.Metrics.IncEvalObservation(evt.ResourceKind, string(obs.Verdict))
	return nil
}

// buildObservation 组装 EvalObservation（不含 judge 信号，由 applyJudge 填充）。
func (s *ObservationService) buildObservation(evt domain.ObservationReferenceEvent, trace port.ObservedTrace) domain.EvalObservation {
	resourceVersion := domain.ResourceParamVersion{Ref: "", Version: ""}
	source := domain.ParamSourceUnknown
	for _, a := range trace.Assignments {
		if a.RevisionID != "" {
			resourceVersion = domain.ResourceParamVersion{Ref: a.RevisionID, Version: a.Variant}
			source = domain.ParamSourceResource
		}
	}
	// TODO(P2)：平台层版本锚点随配置版本机制绑定后回填；当前未知。
	return domain.EvalObservation{
		ID:       uuid.NewString(),
		TraceID:  evt.TraceID,
		Resource: evt.ResourceRef(),
		Param: domain.ParamVersion{
			Platform: domain.PlatformParamVersion{VersionSeq: 0}, // P2 绑定
			Resource: resourceVersion,
			Source:   source,
		},
		CostPerf: domain.CostPerf{
			LatencyMS: trace.LatencyMs,
			Tokens:    trace.TotalTokens,
			CostUSD:   trace.CostUSD,
		},
		Stratum:   "", // TODO(P1b)：租户 tier 接入后填充
		Verdict:   domain.VerdictPass,
		CreatedAt: time.Now().UTC(),
	}
}

// applyJudge 按三维度 rubric 调用 judge 并填充 signals；任一次失败返回错误
// （上层降级），已完成维度不回滚（保留部分信号）。judge 关闭时跳过。
func (s *ObservationService) applyJudge(ctx context.Context, trace port.ObservedTrace, obs *domain.EvalObservation) error {
	if s.deps.Judge == nil || !s.deps.Judge.Enabled(ctx) {
		return nil
	}
	start := time.Now()
	for _, dimension := range observationJudgeDimensions {
		res, err := s.deps.Judge.Judge(ctx, port.JudgeRequest{
			Model:          "",
			Rubric:         judgeRubric(dimension),
			Input:          trace.Input,
			ExpectedOutput: "",
			Actual:         trace.Output,
		})
		if err != nil {
			return fmt.Errorf("judge dimension %s: %w", dimension, err)
		}
		// LLMJudge 契约返回 domain.AssertionResult{Passed, Message}：P1a 把维度
		// 通过映射为 1.0 / 0.0。
		// TODO(P1b)：judge 返回结构化 score/confidence 后填充真实置信度。
		score := 0.0
		if res.Passed {
			score = 1.0
		}
		obs.Signals.Judge = append(obs.Signals.Judge, domain.JudgeSignal{
			Dimension:  dimension,
			Score:      score,
			Confidence: 1.0,
		})
		s.deps.Metrics.RecordEvalJudgeScore(string(obs.Resource.Kind), dimension, score)
	}
	seconds := time.Since(start).Seconds()
	s.deps.Metrics.RecordEvalJudgeLatency(seconds)
	s.deps.Metrics.RecordEvalJudgeCost(trace.CostUSD)
	// 任一维度低于阈值视为 flag（仅信号级，非门禁判定）。
	if anyJudgeBelow(obs.Signals.Judge, 0.5) {
		obs.Verdict = domain.VerdictFlag
	}
	return nil
}

// judgeRubric 构造单维度 judge 提示词（与 judgeAdapter 的 Complete 输出契约
// {"passed","reason"} 对齐：这里的 rubric 指示 LLM 按指定维度判定 pass/不通过）。
func judgeRubric(dimension string) string {
	return fmt.Sprintf("请按维度「%s」对助手回答判定通过/不通过，并给出理由。忠实于给定上下文、切题、覆盖全部关键点。", dimension)
}

func anyJudgeBelow(signals []domain.JudgeSignal, threshold float64) bool {
	for _, s := range signals {
		if s.Score < threshold {
			return true
		}
	}
	return false
}

// ListObservations 分页查询观测明细（查询 API 数据源）。
func (s *ObservationService) ListObservations(ctx context.Context, tenantID, resourceKind, resourceID string,
	from, to *time.Time, limit, offset int,
) ([]domain.EvalObservation, error) {
	if s.deps.Repo == nil {
		return nil, fmt.Errorf("observation service: repository unavailable")
	}
	return s.deps.Repo.QueryByResource(ctx, tenantID, resourceKind, resourceID, from, to, limit, offset)
}

// GetObservation 取单条观测明细。
func (s *ObservationService) GetObservation(ctx context.Context, tenantID, id string) (*domain.EvalObservation, error) {
	if s.deps.Repo == nil {
		return nil, fmt.Errorf("observation service: repository unavailable")
	}
	return s.deps.Repo.Get(ctx, tenantID, id)
}

// JudgeRequest 的预期输出当前留空：运行态观测无 golden（评测集才有 ExpectedOutput）。
