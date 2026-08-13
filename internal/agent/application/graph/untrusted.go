package graph

import (
	"errors"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// ErrInternalToolResultGuardUnavailable is returned when untrusted tool
// content reaches the model without a guard wired in. Guard wiring is the
// agent's only fail-closed backstop for untrusted content, so a nil guard is
// a hard failure rather than a silent pass-through.
var ErrInternalToolResultGuardUnavailable = errors.New("internal tool result guard unavailable")

// WrapUntrustedSection wraps content in a structural untrusted marker so a
// downstream model can distinguish data from instructions. The marker is
// independent of any system prompt wording: even a tenant-controlled prompt
// that never mentions the marker keeps the structural distinction, so an
// injected "ignore prior instructions" inside the section stays inside the
// section instead of being laundered into system-level directives.
func WrapUntrustedSection(label, content string) string {
	return "<untrusted_" + label + ">\n" + content + "\n</untrusted_" + label + ">"
}

// guardUntrustedToolText runs a plain-text tool result through the injected
// guard fn and returns the guarded model content. Unlike
// guardInternalAssistantEvidence, it does NOT pre-marshal the value for a byte
// size check: RAG/recall results are already plain text and can exceed the
// 32KiB JSON bound without being an error — the guard's rune-based truncation
// caps them (truncate-not-fail). A nil fn fails closed.
func guardUntrustedToolText(fn func(any) (port.GuardedToolResult, error), content string) (string, error) {
	if fn == nil {
		return "", ErrInternalToolResultGuardUnavailable
	}
	guarded, err := fn(content)
	if err != nil {
		return "", err
	}
	return guarded.ModelContent, nil
}
