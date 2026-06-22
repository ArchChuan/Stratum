package constants

import "time"

const (
	AccessTokenTTL  = 72 * time.Hour
	RefreshTokenTTL = 7 * 24 * time.Hour
	OnboardingTTL   = 5 * time.Minute
	InviteTokenTTL  = 72 * time.Hour

	// OAuthStateCookieMaxAge is in seconds (http.SetCookie accepts int).
	OAuthStateCookieMaxAge = 300

	// DefaultTenantID is the system tenant for global and system admins.
	DefaultTenantID = "tenant_default"
)
