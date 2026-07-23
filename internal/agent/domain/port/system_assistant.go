package port

import "github.com/byteBuilderX/stratum/internal/agent/domain"

// SystemAssistantProfileCatalog resolves retained, immutable profile versions.
type SystemAssistantProfileCatalog interface {
	Profile(version string) (domain.SystemAssistantProfile, bool)
}
