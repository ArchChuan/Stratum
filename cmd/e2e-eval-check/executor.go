package main

import (
	"context"
	"errors"
)

// infraError marks failures that are the environment's fault (server down,
// auth misconfigured, provider unavailable). Executors construct it; the
// pipeline maps it to exit code 2 via classifyError. (Task 2's client
// migration consolidated this definition and removed the legacy duplicate.)
type infraError struct{ err error }

func (e *infraError) Error() string { return e.err.Error() }
func (e *infraError) Unwrap() error { return e.err }

// isInfra reports whether err is an infrastructure failure.
func isInfra(err error) bool {
	var infra *infraError
	return errors.As(err, &infra)
}

// resourceNotFoundError marks a 404: the point references a resource (agent,
// dataset, workspace) that does not exist on the server. That is a
// dataset/provisioning defect — a broken point no case can pass — not an
// infrastructure break. The llm executor aborts the run on it (fatal, exit 1)
// instead of recording 0%-pass case errors, matching the skill executor's
// fail-closed provisioning. It is deliberately NOT an infraError (exit 2): the
// server is up, the setup is wrong.
type resourceNotFoundError struct{ err error }

func (e *resourceNotFoundError) Error() string { return e.err.Error() }
func (e *resourceNotFoundError) Unwrap() error { return e.err }

// isResourceNotFound reports whether err is a 404 resource-not-found defect.
func isResourceNotFound(err error) bool {
	var rnf *resourceNotFoundError
	return errors.As(err, &rnf)
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
