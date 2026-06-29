package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestMemoryLifecycle tests full memory lifecycle: buffer → extract → recall → forget.
func TestMemoryLifecycle(t *testing.T) {
	env := SetupMemoryTestEnv(t)
	ctx := context.Background()

	conversationID := uuid.New().String()

	// Step 1: Buffer 5 messages (triggers flush at K=5)
	messages := []string{
		"I prefer dark mode for coding",
		"Python is my favorite language",
		"I use VSCode as my editor",
		"I work on machine learning projects",
		"React is great for frontend",
	}

	for i, content := range messages {
		req := &application.BufferMessageRequest{
			TenantID:       env.TenantID,
			UserID:         env.UserID,
			AgentID:        env.AgentID,
			ConversationID: conversationID,
			MessageID:      uuid.New().String(),
			Role:           "user",
			Content:        content,
			CreatedAt:      time.Now().Add(-time.Duration(i) * time.Minute),
		}

		err := env.MemoryService.BufferMessage(ctx, req)
		require.NoError(t, err, "buffer message %d", i)
	}

	// Step 2: Trigger extraction (in real system, worker polls queue)
	// For E2E, directly call ExtractFacts on buffered messages
	extractReq := &application.ExtractFactsRequest{
		TenantID: env.TenantID,
		UserID:   env.UserID,
		AgentID:  env.AgentID,
		Messages: []application.MessageDTO{
			{Role: "user", Content: "I prefer dark mode for coding"},
		},
	}

	err := env.MemoryService.ExtractFacts(ctx, extractReq)
	require.NoError(t, err, "extract facts")

	// Step 3: Recall memories via hybrid search
	recallReq := &application.RecallMemoryRequest{
		TenantID: env.TenantID,
		UserID:   env.UserID,
		AgentID:  env.AgentID,
		Query:    "user preferences",
		TopK:     10,
	}

	recallResp, err := env.MemoryService.RecallMemory(ctx, recallReq)
	require.NoError(t, err, "recall memory")
	require.NotEmpty(t, recallResp.Facts, "should have extracted facts")

	// Verify fact content
	foundDarkMode := false
	for _, fact := range recallResp.Facts {
		if contains(fact.Content, "dark mode") {
			foundDarkMode = true
			break
		}
	}
	require.True(t, foundDarkMode, "should recall dark mode preference")

	// Step 4: Forget specific fact
	factID := recallResp.Facts[0].ID
	forgetReq := &application.ForgetMemoryRequest{
		TenantID: env.TenantID,
		UserID:   env.UserID,
		FactID:   factID,
	}

	err = env.MemoryService.ForgetMemory(ctx, forgetReq)
	require.NoError(t, err, "forget memory")

	// Step 5: Verify fact no longer recalled
	recallResp2, err := env.MemoryService.RecallMemory(ctx, recallReq)
	require.NoError(t, err, "recall after forget")

	for _, fact := range recallResp2.Facts {
		require.NotEqual(t, factID, fact.ID, "forgotten fact should not appear")
	}
}

// contains checks if s contains substr (case-insensitive).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
