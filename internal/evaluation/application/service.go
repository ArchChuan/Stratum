package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type ExecutionResult = port.ExecutionResult

var ErrRunNotFound = errors.New("evaluation run not found")

type RunInput struct {
	TenantID    string
	RequestedBy string
	Resource    domain.ResourceRef
	Suite       domain.EvalSuiteRevision
}

type Service struct {
	adapter     port.ResourceAdapter
	repo        port.RunRepository
	suites      port.SuiteRepository
	traceReader port.TraceEvidenceReader
	// judge evaluates assertion_mode=judge cases; nil keeps rule assertions
	// working and makes judge cases fail closed.
	judge port.LLMJudge
	// review 是人工评审池升级入口（P1c §6.6 内联触发）；nil 时评审升级静默跳过
	// （fail-open，评审池未装配不阻断评测执行）。
	review    port.ReviewEscalator
	reviewCfg domain.ReviewConfig
	logger    *zap.Logger
}

func NewService(
	adapter port.ResourceAdapter,
	repo port.RunRepository,
	traceReader port.TraceEvidenceReader,
	judge port.LLMJudge,
	suites ...port.SuiteRepository,
) *Service {
	var suiteRepo port.SuiteRepository
	if len(suites) > 0 {
		suiteRepo = suites[0]
	}
	return &Service{adapter: adapter, repo: repo, suites: suiteRepo, traceReader: traceReader, judge: judge, logger: zap.NewNop()}
}

// SetReviewEscalator 注入评审池升级器（wiring 在 NewService 之后调用）。
func (s *Service) SetReviewEscalator(e port.ReviewEscalator, cfg domain.ReviewConfig) {
	s.review = e
	s.reviewCfg = cfg
}

// escalateCaseResult 通过评审池升级器判定评测集 judge 结果是否入池并幂等落条目
// （fail-open：失败仅日志，不阻断评测流程）。
func (s *Service) escalateCaseResult(
	ctx context.Context, tenantID, runID string, result domain.EvalCaseResult, c domain.EvalCase, assertion domain.AssertionResult,
) {
	if s.review == nil {
		return
	}
	if err := s.review.TryEscalateCaseResult(ctx, tenantID, runID, result, c, assertion); err != nil {
		s.logReviewEscalateError(ctx, err)
	}
}

func (s *Service) logReviewEscalateError(_ context.Context, err error) {
	s.logger.Warn("evaluation review escalation failed", zap.Error(err))
}

func (s *Service) RunStored(
	ctx context.Context,
	tenantID, requestedBy string,
	resource domain.ResourceRef,
	suiteRevisionID string,
) (domain.EvalRun, error) {
	if s.suites == nil {
		return domain.EvalRun{}, errors.New("evaluation suite repository not configured")
	}
	suite, ok, err := s.suites.GetRevision(ctx, tenantID, suiteRevisionID)
	if err != nil {
		return domain.EvalRun{}, err
	}
	if !ok || suite.Status != domain.SuiteRevisionPublished {
		return domain.EvalRun{}, ErrSuiteNotFound
	}
	if suite.ResourceKind != resource.Kind {
		return domain.EvalRun{}, fmt.Errorf("evaluation suite resource kind %q does not match %q", suite.ResourceKind, resource.Kind)
	}
	return s.Run(ctx, RunInput{TenantID: tenantID, RequestedBy: requestedBy, Resource: resource, Suite: suite})
}

func (s *Service) GetRun(ctx context.Context, tenantID, runID string) (domain.EvalRun, error) {
	run, ok, err := s.repo.GetRun(ctx, tenantID, runID)
	if err != nil {
		return domain.EvalRun{}, err
	}
	if !ok {
		return domain.EvalRun{}, ErrRunNotFound
	}
	return run, nil
}

func (s *Service) Run(ctx context.Context, input RunInput) (domain.EvalRun, error) {
	if err := input.Resource.Validate(); err != nil {
		return domain.EvalRun{}, err
	}
	run := domain.EvalRun{
		ID:              uuid.Must(uuid.NewV7()).String(),
		Resource:        input.Resource,
		SuiteRevisionID: input.Suite.ID,
		Passed:          true,
		Results:         make([]domain.EvalCaseResult, 0, len(input.Suite.Cases)),
		CreatedAt:       time.Now().UTC(),
	}
	for _, testCase := range input.Suite.Cases {
		if !testCase.Enabled {
			continue
		}
		run.TotalCases++
		result := s.runCase(ctx, input.TenantID, input.RequestedBy, input.Resource, run.ID, testCase)
		if result.Passed {
			run.PassedCases++
		} else {
			run.Passed = false
		}
		run.Results = append(run.Results, result)
	}
	run.Metrics = aggregateRunMetrics(run)
	if err := s.repo.SaveRun(ctx, input.TenantID, run); err != nil {
		return domain.EvalRun{}, err
	}
	return run, nil
}

func (s *Service) runCase(
	ctx context.Context, tenantID, requestedBy string, ref domain.ResourceRef, runID string, testCase domain.EvalCase,
) domain.EvalCaseResult {
	execution, err := s.adapter.ExecuteRevision(ctx, tenantID, requestedBy, ref, testCase)
	result := domain.EvalCaseResult{ID: uuid.Must(uuid.NewV7()).String(), CaseID: testCase.ID}
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Actual = execution.Output
	result.TraceID = execution.TraceID
	result.Tokens = execution.Tokens
	result.CostUSD = execution.CostUSD
	result.DurationMs = execution.DurationMs
	result.RAGEvidence = execution.RAGEvidence

	// Resolve trace evidence from Opik (best-effort: Opik unavailability must
	// not block Agent execution or evaluation).
	if execution.TraceID != "" && s.traceReader != nil {
		trace, resolveErr := s.traceReader.Resolve(ctx, tenantID, execution.TraceID)
		if resolveErr != nil {
			// warn-only: trace evidence is supplementary, not critical
			result.Message = "trace evidence unavailable"
		} else {
			result.TraceEvidence = observedTraceToEvidence(trace)
		}
	}

	// Judge assertions dispatch to the LLM judge port; rule assertions stay
	// in the domain's pure EvaluateAssertion.
	if testCase.AssertionMode == domain.AssertionJudge {
		assertion, result := s.judgeCase(ctx, testCase, result)
		// 评审池内联触发（P1c §6.6）：仅 judge 实际产出判定（result.Error 为空）时
		// 才可能入池；judge 关闭/故障是基础设施失败，不是评审信号。
		if result.Error == "" {
			s.escalateCaseResult(ctx, tenantID, runID, result, testCase, assertion)
		}
		return result
	}

	assertion, err := domain.EvaluateAssertion(testCase.AssertionMode, execution.Output, testCase.ExpectedOutput)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Passed = assertion.Passed
	result.Message = assertion.Message
	return result
}

// judgeCase runs the LLM judge assertion for a judge case. Fail-closed: a
// nil or disabled judge makes the case fail with an explicit error instead
// of a silent pass. It returns the raw assertion (for review-pool escalation)
// alongside the result.
func (s *Service) judgeCase(ctx context.Context, testCase domain.EvalCase, result domain.EvalCaseResult) (domain.AssertionResult, domain.EvalCaseResult) {
	var zero domain.AssertionResult
	if s.judge == nil || !s.judge.Enabled(ctx) {
		result.Error = "LLM judge disabled"
		return zero, result
	}
	inputJSON, err := json.Marshal(testCase.Input)
	if err != nil {
		result.Error = fmt.Errorf("judge: marshal input: %w", err).Error()
		return zero, result
	}
	expectedJSON, err := json.Marshal(testCase.ExpectedOutput)
	if err != nil {
		result.Error = fmt.Errorf("judge: marshal expected output: %w", err).Error()
		return zero, result
	}
	actualJSON, err := json.Marshal(result.Actual)
	if err != nil {
		result.Error = fmt.Errorf("judge: marshal actual output: %w", err).Error()
		return zero, result
	}
	var spec domain.JudgeSpec
	if testCase.JudgeSpec != nil {
		spec = *testCase.JudgeSpec
	}
	assertion, err := s.judge.Judge(ctx, port.JudgeRequest{
		Model:          spec.Model,
		Rubric:         spec.Rubric,
		Input:          string(inputJSON),
		ExpectedOutput: string(expectedJSON),
		Actual:         string(actualJSON),
	})
	if err != nil {
		result.Error = err.Error()
		return zero, result
	}
	result.Passed = assertion.Passed
	result.Message = assertion.Message
	return assertion, result
}

func observedTraceToEvidence(t port.ObservedTrace) *domain.ObservedTraceEvidence {
	return &domain.ObservedTraceEvidence{
		CostUSD:           t.CostUSD,
		LatencyMs:         t.LatencyMs,
		Success:           t.Success,
		SecurityViolation: t.SecurityViolation,
	}
}
