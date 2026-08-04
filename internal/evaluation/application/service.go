package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/google/uuid"
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
}

func NewService(
	adapter port.ResourceAdapter,
	repo port.RunRepository,
	traceReader port.TraceEvidenceReader,
	suites ...port.SuiteRepository,
) *Service {
	var suiteRepo port.SuiteRepository
	if len(suites) > 0 {
		suiteRepo = suites[0]
	}
	return &Service{adapter: adapter, repo: repo, suites: suiteRepo, traceReader: traceReader}
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
		result := s.runCase(ctx, input.TenantID, input.RequestedBy, input.Resource, testCase)
		if result.Passed {
			run.PassedCases++
		} else {
			run.Passed = false
		}
		run.Results = append(run.Results, result)
	}
	if err := s.repo.SaveRun(ctx, input.TenantID, run); err != nil {
		return domain.EvalRun{}, err
	}
	return run, nil
}

func (s *Service) runCase(
	ctx context.Context, tenantID, requestedBy string, ref domain.ResourceRef, testCase domain.EvalCase,
) domain.EvalCaseResult {
	execution, err := s.adapter.ExecuteRevision(ctx, tenantID, requestedBy, ref, testCase)
	result := domain.EvalCaseResult{CaseID: testCase.ID}
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Actual = execution.Output
	result.TraceID = execution.TraceID
	result.Tokens = execution.Tokens
	result.CostUSD = execution.CostUSD
	result.DurationMs = execution.DurationMs

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

	assertion, err := domain.EvaluateAssertion(testCase.AssertionMode, execution.Output, testCase.ExpectedOutput)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Passed = assertion.Passed
	result.Message = assertion.Message
	return result
}

func observedTraceToEvidence(t port.ObservedTrace) *domain.ObservedTraceEvidence {
	return &domain.ObservedTraceEvidence{
		CostUSD:           t.CostUSD,
		LatencyMs:         t.LatencyMs,
		Success:           t.Success,
		SecurityViolation: t.SecurityViolation,
	}
}
