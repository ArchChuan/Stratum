package infrastructure

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyExtraHeaders_beforeHardcodedAuth(t *testing.T) {
	h := make(http.Header)
	applyExtraHeaders(h, map[string]string{"X-Tenant": "t1"})
	h.Set("Authorization", "Bearer final")
	require.Equal(t, "Bearer final", h.Get("Authorization"))
	require.Equal(t, "t1", h.Get("X-Tenant"))
}

func TestApplyExtraHeaders_nilSafe(t *testing.T) {
	h := make(http.Header)
	applyExtraHeaders(h, nil)
	require.Empty(t, h)
}

func TestApplyExtraHeaders_overwriteExisting(t *testing.T) {
	h := make(http.Header)
	h.Set("X-Custom", "old")
	applyExtraHeaders(h, map[string]string{"X-Custom": "new"})
	require.Equal(t, "new", h.Get("X-Custom"))
}
