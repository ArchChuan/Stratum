package milvus

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIsRateLimit pins the exact production marker so a Milvus proxy upgrade
// that changes wording is caught rather than silently mis-classified. The
// rate-limited flush is what backs the ingest flush retry loop.
func TestIsRateLimit(t *testing.T) {
	require.True(t, isRateLimit(errors.New("request is rejected by grpc RateLimiter middleware, please retry later: rate limit exceeded[rate=0.1]")))
	require.True(t, isRateLimit(errors.New("RATE LIMIT exceeded")))
	require.False(t, isRateLimit(errors.New("milvus proxy is not ready")))
	require.False(t, isRateLimit(errors.New("collection not found")))
}
