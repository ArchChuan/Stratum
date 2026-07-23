package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const contractRetryBackoff = 25 * time.Millisecond

type ContractErrorCategory string

const (
	ContractErrorUnavailableTool ContractErrorCategory = "unavailable_tool"
	ContractErrorMalformedOutput ContractErrorCategory = "malformed_output"
	ContractErrorSchemaDrift     ContractErrorCategory = "schema_drift"
	ContractErrorTimeout         ContractErrorCategory = "timeout"
	ContractErrorRetryExhausted  ContractErrorCategory = "retry_exhausted"
)

type ContractSnapshot struct {
	EnabledTools         []string
	Timeout              time.Duration
	MaxRetries           int
	ExpectedSchemaHash   string
	DiscoveredSchemaHash string
}

type ContractCase struct {
	ToolName  string
	Arguments map[string]any
}

type ContractResult struct {
	Output        any
	DurationMs    int
	ErrorCategory ContractErrorCategory
}

type ContractInvoker func(context.Context, string, map[string]any) (any, error)

func EvaluateContract(
	ctx context.Context, snapshot ContractSnapshot, testCase ContractCase, invoke ContractInvoker,
) (ContractResult, error) {
	started := time.Now()
	fail := func(category ContractErrorCategory) (ContractResult, error) {
		return ContractResult{DurationMs: int(time.Since(started).Milliseconds()), ErrorCategory: category},
			fmt.Errorf("MCP contract evaluation failed: %s", category)
	}
	testCase.ToolName = strings.TrimSpace(testCase.ToolName)
	if testCase.ToolName == "" || !slices.Contains(snapshot.EnabledTools, testCase.ToolName) {
		return fail(ContractErrorUnavailableTool)
	}
	if snapshot.ExpectedSchemaHash != "" && snapshot.ExpectedSchemaHash != snapshot.DiscoveredSchemaHash {
		return fail(ContractErrorSchemaDrift)
	}
	if invoke == nil {
		return fail(ContractErrorUnavailableTool)
	}
	budgetCtx, cancelBudget := context.WithTimeout(ctx, snapshot.Timeout)
	defer cancelBudget()
	var output any
	for attempt := 0; attempt <= snapshot.MaxRetries; attempt++ {
		var err error
		output, err = invoke(budgetCtx, testCase.ToolName, testCase.Arguments)
		deadlineReached := errors.Is(budgetCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded)
		if err == nil {
			if _, marshalErr := json.Marshal(output); marshalErr != nil {
				return fail(ContractErrorMalformedOutput)
			}
			return ContractResult{Output: map[string]any{"status": "success"},
				DurationMs: int(time.Since(started).Milliseconds())}, nil
		}
		if deadlineReached && attempt == snapshot.MaxRetries {
			return fail(ContractErrorTimeout)
		}
		if !deadlineReached && attempt == snapshot.MaxRetries {
			return fail(ContractErrorRetryExhausted)
		}
		timer := time.NewTimer(contractRetryBackoff)
		select {
		case <-budgetCtx.Done():
			timer.Stop()
			return fail(ContractErrorTimeout)
		case <-timer.C:
		}
	}
	return fail(ContractErrorRetryExhausted)
}
