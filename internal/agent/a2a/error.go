// Package a2a provides agent-to-agent communication and orchestration.
package a2a

import "fmt"

// ErrorType defines the type of A2A error
type ErrorType string

const (
	ErrorTypeDiscovery     ErrorType = "discovery"
	ErrorTypeNegotiation   ErrorType = "negotiation"
	ErrorTypeOrchestration ErrorType = "orchestration"
	ErrorTypeProtocol      ErrorType = "protocol"
	ErrorTypeTimeout       ErrorType = "timeout"
	ErrorTypeValidation    ErrorType = "validation"
)

// A2AError represents an A2A protocol error
type A2AError struct {
	Type    ErrorType
	Message string
	Cause   error
}

// Error implements the error interface
func (e *A2AError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Type, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", e.Type, e.Message)
}

// Unwrap returns the underlying cause
func (e *A2AError) Unwrap() error {
	return e.Cause
}

// NewError creates a new A2A error
func NewError(errorType ErrorType, message string, cause error) *A2AError {
	return &A2AError{
		Type:    errorType,
		Message: message,
		Cause:   cause,
	}
}
