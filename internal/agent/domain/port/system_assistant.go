package port

import "github.com/byteBuilderX/stratum/internal/agent/domain"

// SystemAssistantProfileSource exposes one selected immutable runtime profile.
// Implementations must return defensive value copies.
type SystemAssistantProfileSource interface {
	Profile() domain.SystemAssistantProfile
	Version() string
}
