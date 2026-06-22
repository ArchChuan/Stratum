package e2e

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/stretchr/testify/require"
)

// TestMemoryAdminDiagnostics tests GetDiagnostics and cross-tenant operations.
func TestMemoryAdminDiagnostics(t *testing.T) {
	env1 := SetupMemoryTestEnv(t)
	env2 := SetupMemoryTestEnv(t)
	ctx := context.Background()

	// Step 1: Create facts in tenant 1
	extractReq1 := &application.ExtractFactsRequest{
		TenantID: env1.TenantID,
		UserID:   env1.UserID,
		AgentID:  env1.AgentID,
		Messages: []application.MessageDTO{
			{Role: "user", Content: "I prefer dark mode"},
			{Role: "user", Content: "I use Python"},
		},
	}
	err := env1.MemoryService.ExtractFacts(ctx, extractReq1)
	require.NoError(t, err, "extract facts tenant 1")

	// Step 2: Create facts in tenant 2
	extractReq2 := &application.ExtractFactsRequest{
		TenantID: env2.TenantID,
		UserID:   env2.UserID,
		AgentID:  env2.AgentID,
		Messages: []application.MessageDTO{
			{Role: "user", Content: "I like light mode"},
		},
	}
	err = env2.MemoryService.ExtractFacts(ctx, extractReq2)
	require.NoError(t, err, "extract facts tenant 2")

	// Step 3: Get diagnostics for tenant 1
	diagSvc := application.NewDiagnosticsService(
		env1.FactRepo,
		env1.EntityRepo,
		env1.Queue,
	)

	diag, err := diagSvc.GetDiagnostics(ctx, env1.TenantID)
	require.NoError(t, err, "get diagnostics")

	// Step 4: Verify metrics
	require.GreaterOrEqual(t, diag.ActiveFactCount, 1, "should have active facts")
	require.GreaterOrEqual(t, diag.SupersededCount, 0, "superseded count should be non-negative")
	require.GreaterOrEqual(t, diag.QueueLag, 0, "queue lag should be non-negative")

	// Step 5: Verify top entities
	if len(diag.TopEntities) > 0 {
		require.NotEmpty(t, diag.TopEntities[0].Name, "top entity should have name")
		require.Greater(t, diag.TopEntities[0].Count, 0, "top entity should have fact count")
	}

	// Step 6: Test cross-tenant forget (should fail - IAM scope check)
	// Get a fact ID from tenant 1
	recallReq := &application.RecallMemoryRequest{
		TenantID: env1.TenantID,
		UserID:   env1.UserID,
		AgentID:  env1.AgentID,
		Query:    "preferences",
		TopK:     1,
	}
	recallResp, err := env1.MemoryService.RecallMemory(ctx, recallReq)
	require.NoError(t, err, "recall memory")

	if len(recallResp.Facts) > 0 {
		factID := recallResp.Facts[0].ID

		// Try to forget using tenant 2's service (different user)
		forgetReq := &application.ForgetMemoryRequest{
			TenantID: env2.TenantID,
			UserID:   env2.UserID, // Different user
			FactID:   factID,      // Tenant 1's fact
		}

		err = env2.MemoryService.ForgetMemory(ctx, forgetReq)
		// Should fail with "not found" or "scope mismatch" error
		require.Error(t, err, "cross-tenant forget should fail")
	}
}
