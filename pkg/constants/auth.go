package constants

import "time"

const (
	AccessTokenTTL   = 72 * time.Hour
	RefreshTokenTTL  = 7 * 24 * time.Hour
	OnboardingTTL    = 5 * time.Minute
	OAuthExchangeTTL = 2 * time.Minute
	InviteTokenTTL   = 72 * time.Hour

	// OAuthStateCookieMaxAge is in seconds (http.SetCookie accepts int).
	OAuthStateCookieMaxAge = 300

	// DefaultTenantID is the system tenant for global and system admins.
	DefaultTenantID = "tenant_default"

	// GuestAccountTTL is how long a temporary guest account stays valid before reaping.
	GuestAccountTTL = 24 * time.Hour
	// GuestReaperInterval is how often the background reaper scans for expired guests.
	GuestReaperInterval = time.Hour
	// GuestGitHubIDPrefix namespaces synthetic guest identities so they never
	// collide with numeric GitHub IDs in the users.github_id UNIQUE column.
	GuestGitHubIDPrefix = "guest:"
)
