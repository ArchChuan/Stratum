package port

import (
	"context"
	"encoding/json"
	"time"
)

// PlatformValue is one stored platform-layer parameter value with audit
// metadata (platform_settings row).
type PlatformValue struct {
	Key       string
	Value     json.RawMessage
	UpdatedBy string
	UpdatedAt time.Time
}

// PlatformStore persists platform-scope parameter values in the public
// platform_settings table. All SQL uses schema-qualified names (startup-path
// rule); this store is public-scope by nature and never routes through
// execTenant.
type PlatformStore interface {
	// GetValue returns the stored value for a key, or (false, nil) when the
	// key is absent (absent == unset == definition default applies).
	GetValue(ctx context.Context, key string) (json.RawMessage, bool, error)
	// SetValue upserts one key's value (merge semantics live in the service;
	// the store only ever touches the given key).
	SetValue(ctx context.Context, key string, value json.RawMessage, updatedBy string) error
	// GetAll returns every stored platform value keyed by registry key.
	GetAll(ctx context.Context) ([]PlatformValue, error)
}
