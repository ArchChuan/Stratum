package observe

import (
	"fmt"
	"strings"
	"time"
)

const EventVersion = 1

type Kind string

const (
	KindToolCall     Kind = "tool_call"
	KindSessionReady Kind = "session_ready"
)

type Outcome string

const (
	OutcomeSuccess      Outcome = "success"
	OutcomeError        Outcome = "error"
	OutcomeTimeout      Outcome = "timeout"
	OutcomeCancelled    Outcome = "cancelled"
	OutcomeDisconnected Outcome = "disconnected"
)

type Metadata struct {
	Client         string
	Service        string
	SessionHash    string
	RepositoryHash string
}

type Event struct {
	Version         int       `json:"version"`
	Kind            Kind      `json:"kind"`
	At              time.Time `json:"at"`
	Client          string    `json:"client"`
	Service         string    `json:"service"`
	Tool            string    `json:"tool,omitempty"`
	SessionHash     string    `json:"session_hash"`
	RepositoryHash  string    `json:"repository_hash,omitempty"`
	Outcome         Outcome   `json:"outcome,omitempty"`
	Effective       bool      `json:"effective,omitempty"`
	DurationMS      int64     `json:"duration_ms"`
	ResponseBytes   int       `json:"response_bytes"`
	ConcurrentCalls int       `json:"concurrent_calls,omitempty"`
}

func (e Event) Validate() error {
	if e.Version != EventVersion {
		return fmt.Errorf("version must be %d", EventVersion)
	}
	if e.At.IsZero() {
		return fmt.Errorf("at is required")
	}
	if strings.TrimSpace(e.Client) == "" || strings.TrimSpace(e.Service) == "" || strings.TrimSpace(e.SessionHash) == "" {
		return fmt.Errorf("client, service, and session hash are required")
	}
	if e.DurationMS < 0 || e.ResponseBytes < 0 || e.ConcurrentCalls < 0 {
		return fmt.Errorf("measurements must be nonnegative")
	}
	switch e.Kind {
	case KindToolCall:
		if strings.TrimSpace(e.Tool) == "" {
			return fmt.Errorf("tool is required for tool calls")
		}
		if !validOutcome(e.Outcome) {
			return fmt.Errorf("unknown tool call outcome %q", e.Outcome)
		}
	case KindSessionReady:
		if e.Tool != "" || e.Outcome != "" || e.Effective || e.ConcurrentCalls != 0 {
			return fmt.Errorf("session ready event contains tool call fields")
		}
	default:
		return fmt.Errorf("unknown event kind %q", e.Kind)
	}
	return nil
}

func validOutcome(outcome Outcome) bool {
	switch outcome {
	case OutcomeSuccess, OutcomeError, OutcomeTimeout, OutcomeCancelled, OutcomeDisconnected:
		return true
	default:
		return false
	}
}
