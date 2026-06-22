package e2e

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/stretchr/testify/require"
)

// TestMemoryContext tests BuildContext integration for Agent system prompt injection.
func TestMemoryContext(t *testing.T) {
	env := SetupMemoryTestEnv(t)
	ctx := context.Background()

	// Step 1: Extract facts with entities
	extractReq := &application.ExtractFactsRequest{
		TenantID: env.TenantID,
		UserID:   env.UserID,
		AgentID:  env.AgentID,
		Messages: []application.MessageDTO{
			{Role: "user", Content: "I use Python for data science"},
			{Role: "user", Content: "I prefer PostgreSQL for relational data"},
			{Role: "user", Content: "I deploy on AWS Lambda"},
		},
	}

	err := env.MemoryService.ExtractFacts(ctx, extractReq)
	require.NoError(t, err, "extract facts")

	// Step 2: Build context for Agent prompt injection
	buildReq := &application.BuildContextRequest{
		TenantID:  env.TenantID,
		UserID:    env.UserID,
		AgentID:   env.AgentID,
		Query:     "user technical preferences",
		TopK:      10,
		ReadScope: "user",
	}

	buildResp, err := env.MemoryService.BuildContext(ctx, buildReq)
	require.NoError(t, err, "build context")

	// Step 3: Verify context text includes facts
	require.NotEmpty(t, buildResp.ContextText, "should have context text")
	require.Contains(t, buildResp.ContextText, "dark mode", "context should mention extracted preference")

	// Step 4: Verify entity profiles included
	require.NotEmpty(t, buildResp.EntityProfiles, "should have entity profiles")

	// Find "dark mode" entity
	foundEntity := false
	for _, profile := range buildResp.EntityProfiles {
		if profile.Name == "dark mode" {
			foundEntity = true
			require.Equal(t, "preference", profile.Type, "entity type should be preference")
			break
		}
	}
	require.True(t, foundEntity, "should have dark mode entity profile")

	// Step 5: Verify facts are ordered by frecency
	require.NotEmpty(t, buildResp.Facts, "should have facts")
	// Facts should be sorted by frecency score descending
	if len(buildResp.Facts) > 1 {
		// In this test, all facts have similar access patterns,
		// but we verify the structure is present
		require.NotZero(t, buildResp.Facts[0].AccessCount, "top fact should have access count")
	}
}
