package constants

import (
	"sync/atomic"
	"time"
)

const (
	AccessTokenTTL     = 72 * time.Hour
	RefreshTokenTTL    = 7 * 24 * time.Hour
	OnboardingTTL      = 5 * time.Minute
	InvitationCodeSize = 32
	OAuthExchangeTTL   = 2 * time.Minute
	InviteTokenTTL     = 72 * time.Hour

	// OAuthStateCookieMaxAge is in seconds (http.SetCookie accepts int).
	OAuthStateCookieMaxAge = 300

	// DefaultTenantID is the well-known literal identifier of the system tenant
	// for global and system admins. The row itself carries a real UUID id
	// (tenants.id defaults to uuid_generate_v4()); management-plane gates must
	// compare against ResolvedDefaultTenantID(), never this literal.
	DefaultTenantID = "tenant_default"

	// GuestAccountTTL is how long a temporary guest account stays valid before reaping.
	GuestAccountTTL = 24 * time.Hour
	// GuestReaperInterval is how often the background reaper scans for expired guests.
	GuestReaperInterval = time.Hour
	// GuestGitHubIDPrefix namespaces synthetic guest identities so they never
	// collide with numeric GitHub IDs in the users.github_id UNIQUE column.
	GuestGitHubIDPrefix = "guest:"
)

// resolvedDefaultTenantID is the real UUID id of the default tenant row,
// resolved once at bootstrap (see cmd/server.BootstrapTenants) and read-only
// afterwards. It starts as the literal DefaultTenantID so unit tests that
// construct tenants with the literal keep working; bootstrap overwrites it with
// the actual id. If bootstrap never runs (startup failed), the gates compare
// against the literal — which no real JWT carries — so the management plane
// fails closed (403) instead of accidentally opening. atomic pointer keeps the
// write race-free against concurrent request goroutines.
var resolvedDefaultTenantID atomic.Pointer[string]

func init() {
	literal := DefaultTenantID
	resolvedDefaultTenantID.Store(&literal)
}

// SetResolvedDefaultTenantID records the real default tenant id after bootstrap.
func SetResolvedDefaultTenantID(id string) {
	resolvedDefaultTenantID.Store(&id)
}

// ResolvedDefaultTenantID returns the default tenant id used by management-plane
// gates (RequireDefaultTenant) and system-role derivation (DeriveSystemRole).
func ResolvedDefaultTenantID() string {
	if p := resolvedDefaultTenantID.Load(); p != nil {
		return *p
	}
	return DefaultTenantID
}
