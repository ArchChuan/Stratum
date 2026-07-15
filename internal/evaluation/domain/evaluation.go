package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type ResourceKind string

const ResourceKindSkill ResourceKind = "skill"

type ResourceRef struct {
	Kind       ResourceKind `json:"kind"`
	ResourceID string       `json:"resource_id"`
	RevisionID string       `json:"revision_id"`
}

func (r ResourceRef) Validate() error {
	if r.Kind == "" {
		return errors.New("resource kind required")
	}
	if strings.TrimSpace(r.ResourceID) == "" {
		return errors.New("resource id required")
	}
	if strings.TrimSpace(r.RevisionID) == "" {
		return errors.New("revision id required")
	}
	return nil
}

type AssertionMode string

const (
	AssertionExact    AssertionMode = "exact"
	AssertionContains AssertionMode = "contains"
	AssertionRegex    AssertionMode = "regex"
)

type AssertionResult struct {
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

type EvalCase struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	Input          any           `json:"input"`
	ExpectedOutput any           `json:"expected_output"`
	AssertionMode  AssertionMode `json:"assertion_mode"`
	Enabled        bool          `json:"enabled"`
}

type EvalSuiteRevision struct {
	ID           string              `json:"id"`
	SuiteID      string              `json:"suite_id"`
	ParentID     string              `json:"parent_id,omitempty"`
	VersionNo    int                 `json:"version_no,omitempty"`
	Status       SuiteRevisionStatus `json:"status"`
	ResourceKind ResourceKind        `json:"resource_kind"`
	Cases        []EvalCase          `json:"cases"`
}

type SuiteRevisionStatus string

const (
	SuiteRevisionDraft     SuiteRevisionStatus = "draft"
	SuiteRevisionPublished SuiteRevisionStatus = "published"
)

type EvalSuite struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	ActiveRevisionID string `json:"active_revision_id,omitempty"`
	DraftRevisionID  string `json:"draft_revision_id,omitempty"`
}

type EvalCaseResult struct {
	CaseID     string  `json:"case_id"`
	Passed     bool    `json:"passed"`
	Message    string  `json:"message,omitempty"`
	Error      string  `json:"error,omitempty"`
	Actual     any     `json:"actual,omitempty"`
	TraceID    string  `json:"trace_id,omitempty"`
	Tokens     int     `json:"tokens"`
	CostUSD    float64 `json:"cost_usd"`
	DurationMs int     `json:"duration_ms"`
}

type EvalRun struct {
	ID              string           `json:"id"`
	Resource        ResourceRef      `json:"resource"`
	SuiteRevisionID string           `json:"suite_revision_id"`
	Passed          bool             `json:"passed"`
	TotalCases      int              `json:"total_cases"`
	PassedCases     int              `json:"passed_cases"`
	Results         []EvalCaseResult `json:"results"`
	CreatedAt       time.Time        `json:"created_at"`
}

type JobStatus string

const (
	JobQueued    JobStatus = "queued"
	JobRunning   JobStatus = "running"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
	JobCancelled JobStatus = "cancelled"
)

const JobTypeEvalRun = "eval_run"

type EvalRunJobPayload struct {
	Resource        ResourceRef `json:"resource"`
	SuiteRevisionID string      `json:"suite_revision_id"`
}

type EvaluationJob struct {
	ID             string            `json:"id"`
	Type           string            `json:"type"`
	Payload        EvalRunJobPayload `json:"payload"`
	Status         JobStatus         `json:"status"`
	Attempts       int               `json:"attempts"`
	IdempotencyKey string            `json:"idempotency_key"`
	ErrorMessage   string            `json:"error_message,omitempty"`
	ResultID       string            `json:"result_id,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
}

type CandidatePatch struct {
	Source         string         `json:"source"`
	ParameterPatch map[string]any `json:"parameter_patch,omitempty"`
	PromptPatch    map[string]any `json:"prompt_patch,omitempty"`
	Rationale      string         `json:"rationale,omitempty"`
}

type OptimizationJob struct {
	ID               string           `json:"id"`
	Baseline         ResourceRef      `json:"baseline"`
	SuiteRevisionID  string           `json:"suite_revision_id"`
	Status           JobStatus        `json:"status"`
	SearchSpace      map[string][]any `json:"search_space"`
	FailureSummaries []string         `json:"failure_summaries,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
}

type OptimizationCandidate struct {
	ID                 string         `json:"id"`
	OptimizationJobID  string         `json:"optimization_job_id"`
	Revision           ResourceRef    `json:"revision"`
	ParentRevisionID   string         `json:"parent_revision_id"`
	Source             string         `json:"source"`
	Rationale          string         `json:"rationale,omitempty"`
	GenerationMetadata map[string]any `json:"generation_metadata,omitempty"`
	EvalRunID          string         `json:"eval_run_id,omitempty"`
	Rank               int            `json:"rank,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
}

type FeedbackRequest struct {
	TraceID           string
	ResourceKind      ResourceKind
	ResourceID        string
	Score             float64
	Outcome           map[string]any
	IdempotencyKey    string
	SecurityViolation bool
}

type EvaluationFeedback struct {
	ID             string         `json:"id"`
	TraceID        string         `json:"trace_id"`
	ResourceKind   ResourceKind   `json:"resource_kind"`
	ResourceID     string         `json:"resource_id"`
	RevisionID     string         `json:"revision_id"`
	Score          float64        `json:"score"`
	Outcome        map[string]any `json:"outcome,omitempty"`
	IdempotencyKey string         `json:"idempotency_key"`
	CreatedAt      time.Time      `json:"created_at"`
}

type OnlineObservation struct {
	Score             float64
	CostUSD           float64
	LatencyMs         int64
	Success           bool
	SecurityViolation bool
}

func EvaluateAssertion(mode AssertionMode, actual, expected any) (AssertionResult, error) {
	switch mode {
	case AssertionExact:
		actualJSON, err := json.Marshal(actual)
		if err != nil {
			return AssertionResult{}, fmt.Errorf("marshal actual output: %w", err)
		}
		expectedJSON, err := json.Marshal(expected)
		if err != nil {
			return AssertionResult{}, fmt.Errorf("marshal expected output: %w", err)
		}
		passed := bytes.Equal(actualJSON, expectedJSON)
		return AssertionResult{Passed: passed, Message: mismatchMessage(passed, "values differ")}, nil
	case AssertionContains:
		actualText, ok := actual.(string)
		if !ok {
			return AssertionResult{}, errors.New("contains assertion requires string actual output")
		}
		expectedText, ok := expected.(string)
		if !ok {
			return AssertionResult{}, errors.New("contains assertion requires string expected output")
		}
		passed := strings.Contains(actualText, expectedText)
		return AssertionResult{Passed: passed, Message: mismatchMessage(passed, "expected text not found")}, nil
	case AssertionRegex:
		actualText, ok := actual.(string)
		if !ok {
			return AssertionResult{}, errors.New("regex assertion requires string actual output")
		}
		pattern, ok := expected.(string)
		if !ok {
			return AssertionResult{}, errors.New("regex assertion requires string pattern")
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return AssertionResult{}, fmt.Errorf("compile assertion regex: %w", err)
		}
		passed := re.MatchString(actualText)
		return AssertionResult{Passed: passed, Message: mismatchMessage(passed, "regular expression did not match")}, nil
	default:
		return AssertionResult{}, fmt.Errorf("unsupported assertion mode: %s", mode)
	}
}

func mismatchMessage(passed bool, message string) string {
	if passed {
		return ""
	}
	return message
}
