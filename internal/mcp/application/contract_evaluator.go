package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

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
	if !slices.Contains(snapshot.EnabledTools, testCase.ToolName) {
		return fail(ContractErrorUnavailableTool)
	}
	if snapshot.ExpectedSchemaHash != "" && snapshot.ExpectedSchemaHash != snapshot.DiscoveredSchemaHash {
		return fail(ContractErrorSchemaDrift)
	}
	if invoke == nil {
		return fail(ContractErrorUnavailableTool)
	}
	var output any
	for attempt := 0; attempt <= snapshot.MaxRetries; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, snapshot.Timeout)
		var err error
		output, err = invoke(callCtx, testCase.ToolName, testCase.Arguments)
		deadlineReached := errors.Is(callCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded)
		cancel()
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
	}
	return fail(ContractErrorRetryExhausted)
}
