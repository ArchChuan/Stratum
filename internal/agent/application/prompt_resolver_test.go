package application

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubPromptRegistry fakes agentport.PromptRegistry; a nil text means the
// registry had no binding and the resolver must fall through.
type stubPromptRegistry struct {
	text string
	err  error
}

func (s stubPromptRegistry) GetEffectivePrompt(
	ctx context.Context, key, tenantID, agentID, requestID string,
) (string, error) {
	return s.text, s.err
}

// stubOverrideRepo fakes PromptOverrideRepo (legacy DB overrides).
type stubOverrideRepo struct {
	overrides map[string]string
	err       error
}

func (s stubOverrideRepo) GetOverrides(ctx context.Context, tenantID string) (map[string]string, error) {
	return s.overrides, s.err
}

func newResolver(registry stubPromptRegistry, overrides stubOverrideRepo) *PromptResolver {
	r := NewPromptResolver(overrides)
	r.SetRegistry(registry)
	return r
}

func TestPromptResolver_registryWinsOverOverride(t *testing.T) {
	r := newResolver(
		stubPromptRegistry{text: "registry text"},
		stubOverrideRepo{overrides: map[string]string{string(PromptKeySystem): "override text"}},
	)

	got, err := r.Resolve(context.Background(), "t1", PromptKeySystem)
	require.NoError(t, err)
	require.Equal(t, "registry text", got)
}

func TestPromptResolver_emptyRegistryFallsThroughToOverride(t *testing.T) {
	r := newResolver(
		stubPromptRegistry{}, // empty text → no binding
		stubOverrideRepo{overrides: map[string]string{string(PromptKeySystem): "override text"}},
	)

	got, err := r.Resolve(context.Background(), "t1", PromptKeySystem)
	require.NoError(t, err)
	require.Equal(t, "override text", got)
}

func TestPromptResolver_registryErrorFallsThroughToOverride(t *testing.T) {
	r := newResolver(
		stubPromptRegistry{err: errors.New("registry down")},
		stubOverrideRepo{overrides: map[string]string{string(PromptKeySystem): "override text"}},
	)

	got, err := r.Resolve(context.Background(), "t1", PromptKeySystem)
	require.NoError(t, err)
	require.Equal(t, "override text", got)
}

func TestPromptResolver_noSourcesReturnsDefault(t *testing.T) {
	r := newResolver(stubPromptRegistry{}, stubOverrideRepo{})

	got, err := r.Resolve(context.Background(), "t1", PromptKeySystem)
	require.NoError(t, err)
	require.NotEmpty(t, got) // embedded default exists
}

func TestPromptResolver_skipsOverridesWithoutTenant(t *testing.T) {
	r := newResolver(
		stubPromptRegistry{},
		stubOverrideRepo{overrides: map[string]string{string(PromptKeySystem): "override text"}},
	)

	got, err := r.Resolve(context.Background(), "", PromptKeySystem)
	require.NoError(t, err)
	require.NotEqual(t, "override text", got)
}

func TestPromptResolver_overrideLoadErrorReturnsDefault(t *testing.T) {
	r := newResolver(
		stubPromptRegistry{},
		stubOverrideRepo{err: errors.New("db down")},
	)

	got, err := r.Resolve(context.Background(), "t1", PromptKeySystem)
	require.ErrorContains(t, err, "load overrides")
	require.NotEmpty(t, got) // default still returned
}

func TestPromptResolver_unknownKeyFails(t *testing.T) {
	r := newResolver(stubPromptRegistry{}, stubOverrideRepo{})

	_, err := r.Resolve(context.Background(), "t1", PromptKey("nope"))
	require.ErrorContains(t, err, "unknown prompt key")
}

func TestPromptResolver_resolveAllCoversEveryKey(t *testing.T) {
	r := newResolver(stubPromptRegistry{}, stubOverrideRepo{})

	got, err := r.ResolveAll(context.Background(), "t1")
	require.NoError(t, err)
	require.Len(t, got, len(AllPromptKeys))
	for _, key := range AllPromptKeys {
		require.NotEmpty(t, got[string(key)], "key %s resolved empty", key)
	}
}
