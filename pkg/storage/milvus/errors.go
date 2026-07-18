package milvus

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
