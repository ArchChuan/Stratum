package milvus

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrCollectionNotFound is returned when a Milvus collection does not exist.
// Callers decide the business semantics with errors.Is: RAG retrieval paths
// fail closed on it (a missing collection with documents in PG is drift),
// while lazy-provisioned memory paths translate it to an empty result. The
// message keeps the "collection not found" wording so legacy string-based
// classification at callers still matches.
var ErrCollectionNotFound = errors.New("milvus collection not found")

// isCollectionNotFound reports whether err means the collection itself is
// missing. "index not found" deliberately does not match: that means the
// collection exists but cannot be loaded, which must surface as a real error
// instead of masquerading as empty data.
func isCollectionNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrCollectionNotFound) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "collection not found") ||
		strings.Contains(msg, "does not exist")
}

// UnavailableError identifies a transient Milvus availability failure.
type UnavailableError struct {
	Op  string
	Err error
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("milvus %s unavailable: %v", e.Op, e.Err)
}

func (e *UnavailableError) Unwrap() error { return e.Err }

func newUnavailableError(op string, err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	return &UnavailableError{Op: op, Err: err}
}

func classifyAvailabilityError(op string, err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return newUnavailableError(op, err)
	}
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return newUnavailableError(op, err)
	case codes.Unknown:
		if isMilvusStartupTransient(err) {
			return newUnavailableError(op, err)
		}
	default:
	}
	return err
}

func isMilvusStartupTransient(err error) bool {
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"milvus proxy is not ready",
		"resource group node not enough",
		"no available query node",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
