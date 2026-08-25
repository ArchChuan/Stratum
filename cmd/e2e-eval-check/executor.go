package main

import (
	"context"
	"errors"
)

// infraError marks failures that are the environment's fault (server down,
// auth misconfigured, provider unavailable). Executors construct it; the
// pipeline maps it to exit code 2 via classifyError. (Task 2's client
// migration reuses this definition and removes rag-check's duplicate.)
type infraError struct{ err error }

func (e *infraError) Error() string { return e.err.Error() }
func (e *infraError) Unwrap() error { return e.err }

// isInfra reports whether err is an infrastructure failure.
func isInfra(err error) bool {
	var infra *infraError
	return errors.As(err, &infra)
}

// execResult is the kind-agnostic outcome an executor returns to the pipeline.
type execResult struct {
	Cases     []caseOutcome
	Aggregate aggregate
	Warnings  []warning
	Residuals []string
	Evidence  []evidence
}

// executor runs one point. Each kind implements this interface.
type executor interface {
	Execute(ctx context.Context, o options, p point) (execResult, error)
}

// newExecutor dispatches on the point kind via the registry. Each kind's
// executor registers its own constructor in init(); this keeps Task 1's
// skeleton compilable before the concrete executors exist.
func newExecutor(kind string) (executor, error) {
	factory, ok := executorRegistry[kind]
	if !ok {
		return nil, errUnsupportedKind(kind)
	}
	return factory(), nil
}

// executorRegistry maps a kind to its executor constructor.
var executorRegistry = map[string]func() executor{}

// registerExecutor registers a kind's constructor; called from each executor
// file's init() in Tasks 2-5.
func registerExecutor(kind string, factory func() executor) {
	executorRegistry[kind] = factory
}

type errUnsupportedKind string

func (e errUnsupportedKind) Error() string { return "unsupported kind " + string(e) }
