package port

import (
	"time"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
)

type TokenClaims struct {
	Sub, TenantID, Role, GlobalRole string
	SystemRole                      domain.SystemRole
	JTI, AvatarURL, GitHubLogin     string
}

type OnboardingClaims struct {
	GitHubID               int64
	GitHubLogin, AvatarURL string
}

type TokenService interface {
	Sign(TokenClaims, time.Duration) (string, error)
	Verify(string) (*TokenClaims, error)
	SignOnboarding(OnboardingClaims, time.Duration) (string, error)
	VerifyOnboarding(string) (*OnboardingClaims, error)
}
