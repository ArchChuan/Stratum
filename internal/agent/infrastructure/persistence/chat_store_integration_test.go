package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
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
	conv, err := store.CreateConversation(ctx, tenantID, domain.SystemAssistantID, "user-1", "artifacts", "manual")
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

// D9：DeleteConversation 在同一租户事务内级联终结关联审批——pending→cancelled、
// approved→voided，reason 均为 conversation_deleted，且物理删除会话后审批历史仍可对账。
// 直接用 SQL 造行：approvals.Create 硬编码 status='pending'，approved 行必须绕过它。
func TestDeleteConversationCascadesApprovals(t *testing.T) {
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
	tenantID := fmt.Sprintf("tmp_chat_cascade_%d", time.Now().UnixNano())
	schema := "tenant_" + tenantID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}

	chat := NewPgChatStore(pool, zap.NewNop())
	chat.SetApprovalCascade(NewPgToolApprovalStore(pool))
	conv, err := chat.CreateConversation(ctx, tenantID, domain.SystemAssistantID, "user-1", "cascade", "manual")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		execID, toolCallID, status string
	}{
		{"exec-pending", "tc-pending", "pending"},
		{"exec-approved", "tc-approved", "approved"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO "`+schema+`".agent_tool_approvals
			 (execution_id, tool_call_id, server_id, tool_name, risk_level, encrypted_payload, conversation_id, status)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			tc.execID, tc.toolCallID, "srv", "delete", "destructive", "enc", conv.ID, tc.status); err != nil {
			t.Fatal(err)
		}
	}

	if err := chat.DeleteConversation(ctx, tenantID, conv.ID, "user-1"); err != nil {
		t.Fatal(err)
	}

	rows, err := pool.Query(ctx,
		`SELECT status, invalidation_reason FROM "`+schema+`".agent_tool_approvals WHERE conversation_id=$1`,
		conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var status, reason string
		if err := rows.Scan(&status, &reason); err != nil {
			t.Fatal(err)
		}
		got = append(got, status+"/"+reason)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{"cancelled/conversation_deleted", "voided/conversation_deleted"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cascade outcomes=%v, want %v", got, want)
	}
}

// D9 回滚：非 owner 删除（0 行 → ErrNotFound）使 DeleteConversation 整体失败，
// 同一租户事务内的级联必须回滚——审批不被终结，历史不可被未授权删除污染。
func TestDeleteConversationCascadeRollsBackOnFailure(t *testing.T) {
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
	tenantID := fmt.Sprintf("tmp_chat_cascade_rollback_%d", time.Now().UnixNano())
	schema := "tenant_" + tenantID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}

	chat := NewPgChatStore(pool, zap.NewNop())
	chat.SetApprovalCascade(NewPgToolApprovalStore(pool))
	conv, err := chat.CreateConversation(ctx, tenantID, domain.SystemAssistantID, "user-1", "rollback", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO "`+schema+`".agent_tool_approvals
		 (execution_id, tool_call_id, server_id, tool_name, risk_level, encrypted_payload, conversation_id, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		"exec-approved", "tc-approved", "srv", "delete", "destructive", "enc", conv.ID, "approved"); err != nil {
		t.Fatal(err)
	}

	if err := chat.DeleteConversation(ctx, tenantID, conv.ID, "wrong-user"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("non-owner delete err=%v, want ErrNotFound", err)
	}

	var status, reason string
	if err := pool.QueryRow(ctx,
		`SELECT status, invalidation_reason FROM "`+schema+`".agent_tool_approvals WHERE conversation_id=$1`, conv.ID).
		Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "approved" || reason != "" {
		t.Fatalf("approval after failed delete=%s/%q, want approved/（未被级联）", status, reason)
	}
}
