package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func TestCheckpointLifecycleRealPostgres(t *testing.T) {
	url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("STRATUM_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	tenantID := fmt.Sprintf("tmp_cp_lifecycle_%d", time.Now().UnixNano())
	schema := "tenant_" + tenantID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	store := NewPgCheckpointStore(pool)

	// persist
	identity := domain.AgentExecutionCheckpoint{
		ExecutionID:            "exec-lifecycle",
		TraceID:                "trace-lifecycle",
		ConversationID:         "11111111-1111-1111-1111-111111111111",
		AgentID:                "agent-lifecycle",
		UserID:                 "user-lifecycle",
		CurrentNode:            "tool_execution",
		StepIndex:              5,
		MessagesSnapshotJSON:   json.RawMessage(`[{"role":"user","content":"hello"}]`),
		PendingToolCallsJSON:   json.RawMessage(`[]`),
		CompletedToolCallsJSON: json.RawMessage(`[{"id":"done-1"}]`),
		RuntimeStateJSON:       json.RawMessage(`{"plan":"active"}`),
		Status:                 "running",
		ExpiresAt:              time.Now().Add(time.Hour),
	}
	if err := store.Upsert(ctx, tenantID, identity); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// resume (GetLatest)
	restored, err := store.GetLatest(ctx, tenantID, "exec-lifecycle")
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if restored.StepIndex != 5 || restored.CurrentNode != "tool_execution" {
		t.Fatalf("resumed checkpoint mismatch: step=%d node=%s", restored.StepIndex, restored.CurrentNode)
	}

	// complete
	if err := store.MarkCompleted(ctx, tenantID, "exec-lifecycle"); err != nil {
		t.Fatalf("mark completed: %v", err)
	}

	// verify completed checkpoint is NOT deleted by DeleteExpired
	t.Run("completed not deleted", func(t *testing.T) {
		deleted, err := store.DeleteExpired(ctx, tenantID)
		if err != nil {
			t.Fatalf("delete expired: %v", err)
		}
		if deleted != 0 {
			t.Fatalf("expected 0 deleted (completed), got %d", deleted)
		}
	})

	// insert expired running checkpoint
	t.Run("expired running is deleted", func(t *testing.T) {
		expiredID := "exec-expired"
		expired := domain.AgentExecutionCheckpoint{
			ExecutionID:    expiredID,
			TraceID:        "trace-expired",
			ConversationID: "22222222-2222-2222-2222-222222222222",
			AgentID:        "agent-lifecycle",
			UserID:         "user-lifecycle",
			CurrentNode:    "llm",
			StepIndex:      1,
			Status:         "running",
			ExpiresAt:      time.Now().Add(-time.Hour), // expired
		}
		if err := store.Upsert(ctx, tenantID, expired); err != nil {
			t.Fatalf("upsert expired: %v", err)
		}
		deleted, err := store.DeleteExpired(ctx, tenantID)
		if err != nil {
			t.Fatalf("delete expired: %v", err)
		}
		if deleted != 1 {
			t.Fatalf("expected 1 deleted, got %d", deleted)
		}
	})

	// empty conversation_id roundtrip (checkpoint 全开后 evaluation run 无
	// conversation:Upsert 必须写 NULL 而非空字符串,GetLatest 读回空串)
	t.Run("empty conversation id roundtrip", func(t *testing.T) {
		noConv := domain.AgentExecutionCheckpoint{
			ExecutionID: "exec-no-conv", TraceID: "trace-no-conv",
			ConversationID: "", AgentID: "agent-lifecycle", UserID: "user-lifecycle",
			CurrentNode: "llm", StepIndex: 3, Status: "running",
			ExpiresAt: time.Now().Add(time.Hour),
		}
		if err := store.Upsert(ctx, tenantID, noConv); err != nil {
			t.Fatalf("upsert no-conv: %v", err)
		}
		restored, err := store.GetLatest(ctx, tenantID, "exec-no-conv")
		if err != nil {
			t.Fatalf("get latest no-conv: %v", err)
		}
		if restored.ConversationID != "" {
			t.Fatalf("expected empty conversation_id, got %q", restored.ConversationID)
		}
	})

	// upsert replaces previous row (ON CONFLICT on execution_id)
	t.Run("upsert replaces previous", func(t *testing.T) {
		replaced := domain.AgentExecutionCheckpoint{
			ExecutionID:    "exec-lifecycle",
			TraceID:        "trace-lifecycle",
			ConversationID: "11111111-1111-1111-1111-111111111111",
			AgentID:        "agent-lifecycle",
			UserID:         "user-lifecycle",
			CurrentNode:    "final",
			StepIndex:      10,
			Status:         "running",
			ExpiresAt:      time.Now().Add(time.Hour),
		}
		if err := store.Upsert(ctx, tenantID, replaced); err != nil {
			t.Fatalf("upsert replace: %v", err)
		}
		latest, err := store.GetLatest(ctx, tenantID, "exec-lifecycle")
		if err != nil {
			t.Fatalf("get latest after replace: %v", err)
		}
		if latest.StepIndex != 10 || latest.CurrentNode != "final" {
			t.Fatalf("expected replaced state, got step=%d node=%s", latest.StepIndex, latest.CurrentNode)
		}
	})
}
