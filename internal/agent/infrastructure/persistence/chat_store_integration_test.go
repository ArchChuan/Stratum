package persistence

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func TestPgChatStoreArtifactsRealPostgresRoundTripAndHistoricalUpgrade(t *testing.T) {
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
	tenantID := fmt.Sprintf("tmp_chat_artifacts_%d", time.Now().UnixNano())
	schema := "tenant_" + tenantID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}

	store := NewPgChatStore(pool, zap.NewNop())
	conv, err := store.CreateConversation(ctx, tenantID, domain.SystemAssistantID, "user-1", "artifacts")
	if err != nil {
		t.Fatal(err)
	}
	invalid := &domain.ChatMessage{ConversationID: conv.ID, Role: "assistant", Content: "invalid", Artifacts: []domain.ExecutionArtifact{{Type: "diagnostic_report", ProfileVersion: "v1", DiagnosticReport: &domain.DiagnosticReport{Inferences: []string{"password=secret"}}}}}
	if err := store.AddMessage(ctx, tenantID, invalid); err == nil {
		t.Fatal("invalid artifact write must fail")
	}
	var invalidRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM "`+schema+`".chat_messages WHERE conversation_id=$1 AND content='invalid'`, conv.ID).Scan(&invalidRows); err != nil {
		t.Fatal(err)
	}
	if invalidRows != 0 {
		t.Fatalf("invalid artifact write persisted %d rows", invalidRows)
	}
	artifacts := []domain.ExecutionArtifact{
		{Type: "citations", ProfileVersion: "v1", Citations: []domain.Citation{{DocumentID: "doc-1", Title: "guide"}}},
		{Type: "diagnostic_report", ProfileVersion: "v1", DiagnosticReport: &domain.DiagnosticReport{Facts: []domain.DiagnosticFact{}, Inferences: []string{}, EvidenceGaps: []domain.EvidenceGap{{Source: "stratum_diagnose_tenant", Code: "timeout"}}, RecommendedActions: []string{}, Citations: []domain.Citation{}, Steps: []domain.DiagnosticStep{{Tool: "stratum_diagnose_tenant", Outcome: "error", ErrorCode: "timeout", LatencyMs: 15}}}},
	}
	if err := store.AddMessage(ctx, tenantID, &domain.ChatMessage{ConversationID: conv.ID, Role: "assistant", Content: "bounded", Artifacts: artifacts, SkipOutbox: true}); err != nil {
		t.Fatal(err)
	}
	got, err := store.ListMessages(ctx, tenantID, conv.ID, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0].Artifacts, artifacts) {
		t.Fatalf("artifact round trip mismatch: %#v", got)
	}

	var historicalID string
	if err := pool.QueryRow(ctx, `INSERT INTO "`+schema+`".chat_messages (conversation_id, role, content) VALUES ($1,'assistant','old') RETURNING id`, conv.ID).Scan(&historicalID); err != nil {
		t.Fatal(err)
	}
	got, err = store.ListMessages(ctx, tenantID, conv.ID, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if got[1].Artifacts == nil || len(got[1].Artifacts) != 0 {
		t.Fatalf("historical artifacts=%#v, want []", got[1].Artifacts)
	}

	if _, err := pool.Exec(ctx, `UPDATE "`+schema+`".chat_messages SET artifacts_json='{}'::jsonb WHERE id=$1`, historicalID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListMessages(ctx, tenantID, conv.ID, "user-1"); err == nil {
		t.Fatal("malformed artifact domain shape must return error")
	}
	if _, err := pool.Exec(ctx, `UPDATE "`+schema+`".chat_messages SET artifacts_json='null'::jsonb WHERE id=$1`, historicalID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListMessages(ctx, tenantID, conv.ID, "user-1"); err == nil {
		t.Fatal("null artifacts must return error")
	}
	if _, err := pool.Exec(ctx, `UPDATE "`+schema+`".chat_messages SET artifacts_json='[{"type":"citations","profileVersion":"v1","citations":[],"unknown":true}]'::jsonb WHERE id=$1`, historicalID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListMessages(ctx, tenantID, conv.ID, "user-1"); err == nil {
		t.Fatal("unknown artifact field must return error")
	}

	var defaultExpr string
	if err := pool.QueryRow(ctx, `SELECT column_default FROM information_schema.columns WHERE table_schema=$1 AND table_name='chat_messages' AND column_name='artifacts_json'`, schema).Scan(&defaultExpr); err != nil {
		t.Fatal(err)
	}
	if defaultExpr == "" {
		t.Fatal("artifacts_json default missing after repeated provision")
	}
}
