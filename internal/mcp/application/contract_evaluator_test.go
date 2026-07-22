package application

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEvaluateContractClassifiesFailuresWithoutRawProviderErrors(t *testing.T) {
	tests := []struct {
		name      string
		contract  ContractSnapshot
		invoke    ContractInvoker
		wantClass ContractErrorCategory
	}{
		{name: "unavailable tool", contract: ContractSnapshot{EnabledTools: []string{"allowed"}, Timeout: time.Second},
			invoke:    func(context.Context, string, map[string]any) (any, error) { return nil, nil },
			wantClass: ContractErrorUnavailableTool},
		{name: "schema drift", contract: ContractSnapshot{EnabledTools: []string{"tool"}, Timeout: time.Second,
			ExpectedSchemaHash: "old", DiscoveredSchemaHash: "new"},
			invoke:    func(context.Context, string, map[string]any) (any, error) { return nil, nil },
			wantClass: ContractErrorSchemaDrift},
		{name: "retry exhausted", contract: ContractSnapshot{EnabledTools: []string{"tool"}, Timeout: time.Second, MaxRetries: 1},
			invoke: func(context.Context, string, map[string]any) (any, error) {
				return nil, errors.New("upstream secret response body")
			}, wantClass: ContractErrorRetryExhausted},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := EvaluateContract(context.Background(), tc.contract,
				ContractCase{ToolName: "tool", Arguments: map[string]any{}}, tc.invoke)
			if err == nil || result.ErrorCategory != tc.wantClass {
				t.Fatalf("category=%q err=%v", result.ErrorCategory, err)
			}
			if err.Error() == "upstream secret response body" {
				t.Fatal("raw provider error escaped")
			}
		})
	}
}

func TestEvaluateContractRetriesThenReturnsSafeOutput(t *testing.T) {
	calls := 0
	result, err := EvaluateContract(context.Background(), ContractSnapshot{
		EnabledTools: []string{"tool"}, Timeout: time.Second, MaxRetries: 1,
	}, ContractCase{ToolName: "tool", Arguments: map[string]any{"id": "1"}},
		func(context.Context, string, map[string]any) (any, error) {
			calls++
			if calls == 1 {
				return nil, errors.New("temporary")
			}
			return map[string]any{"ok": true}, nil
		})
	if err != nil || calls != 2 || result.ErrorCategory != "" {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
	}
}

func TestEvaluateContractClassifiesTimeoutAndMalformedOutput(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		result, err := EvaluateContract(context.Background(), ContractSnapshot{
			EnabledTools: []string{"tool"}, Timeout: time.Millisecond,
		}, ContractCase{ToolName: "tool", Arguments: map[string]any{}},
			func(ctx context.Context, _ string, _ map[string]any) (any, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			})
		if err == nil || result.ErrorCategory != ContractErrorTimeout {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})

	t.Run("malformed output", func(t *testing.T) {
		result, err := EvaluateContract(context.Background(), ContractSnapshot{
			EnabledTools: []string{"tool"}, Timeout: time.Second,
		}, ContractCase{ToolName: "tool", Arguments: map[string]any{}},
			func(context.Context, string, map[string]any) (any, error) {
				return map[string]any{"unsupported": make(chan int)}, nil
			})
		if err == nil || result.ErrorCategory != ContractErrorMalformedOutput {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
}
