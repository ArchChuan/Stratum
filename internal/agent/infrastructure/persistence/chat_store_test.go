package persistence

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/pashagolub/pgxmock/v2"
	"go.uber.org/zap"
)

// newChatStoreWithMock returns a PgChatStore backed by pgxmock.
func newChatStoreWithMock(t *testing.T) (*PgChatStore, pgxmock.PgxPoolIface) {
	t.Helper()
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	return &PgChatStore{pool: pool, logger: zap.NewNop()}, pool
}

// expectTenantTx expects BEGIN + SET LOCAL search_path for tenant t1.
func expectTenantTx(mock pgxmock.PgxPoolIface) {
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
}

func TestChatStore_CreateConversation(t *testing.T) {
	store, mock := newChatStoreWithMock(t)
	defer mock.Close()

	now := time.Now()
	expectTenantTx(mock)
	mock.ExpectQuery("INSERT INTO chat_conversations").
		WithArgs("agent-1", "user-1", "新会话").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "agent_id", "user_id", "name", "created_at", "updated_at", "expires_at",
		}).AddRow("conv-uuid", "agent-1", "user-1", "新会话", now, now, now.AddDate(0, 0, 30)))
	mock.ExpectCommit()

	conv, err := store.CreateConversation(context.Background(), "t1", "agent-1", "user-1", "新会话")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conv.ID != "conv-uuid" {
		t.Errorf("want conv-uuid, got %s", conv.ID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet: %v", err)
	}
}

func TestChatStore_ListConversations(t *testing.T) {
	store, mock := newChatStoreWithMock(t)
	defer mock.Close()

	now := time.Now()
	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, agent_id, user_id, name").
		WithArgs("agent-1", "user-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "agent_id", "user_id", "name", "created_at", "updated_at", "expires_at",
		}).
			AddRow("c1", "agent-1", "user-1", "Chat A", now, now, now.AddDate(0, 0, 30)).
			AddRow("c2", "agent-1", "user-1", "Chat B", now, now, now.AddDate(0, 0, 30)))
	mock.ExpectCommit()

	convs, err := store.ListConversations(context.Background(), "t1", "agent-1", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(convs) != 2 {
		t.Errorf("want 2 conversations, got %d", len(convs))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet: %v", err)
	}
}

func TestChatStore_RenameConversation_success(t *testing.T) {
	store, mock := newChatStoreWithMock(t)
	defer mock.Close()

	expectTenantTx(mock)
	mock.ExpectExec("UPDATE chat_conversations").
		WithArgs("新名字", "conv-1", "user-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	if err := store.RenameConversation(context.Background(), "t1", "conv-1", "user-1", "新名字"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet: %v", err)
	}
}

func TestChatStore_RenameConversation_notFound(t *testing.T) {
	store, mock := newChatStoreWithMock(t)
	defer mock.Close()

	expectTenantTx(mock)
	mock.ExpectExec("UPDATE chat_conversations").
		WithArgs("新名字", "no-such", "user-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()

	err := store.RenameConversation(context.Background(), "t1", "no-such", "user-1", "新名字")
	if err == nil {
		t.Fatal("expected error for missing conversation")
	}
}

func TestChatStore_DeleteConversation_success(t *testing.T) {
	store, mock := newChatStoreWithMock(t)
	defer mock.Close()

	expectTenantTx(mock)
	mock.ExpectExec("DELETE FROM chat_messages").
		WithArgs("conv-1").
		WillReturnResult(pgxmock.NewResult("DELETE", 3))
	mock.ExpectExec("DELETE FROM chat_conversations").
		WithArgs("conv-1", "user-1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()

	if err := store.DeleteConversation(context.Background(), "t1", "conv-1", "user-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet: %v", err)
	}
}

func TestChatStore_DeleteConversation_notOwned(t *testing.T) {
	store, mock := newChatStoreWithMock(t)
	defer mock.Close()

	expectTenantTx(mock)
	mock.ExpectExec("DELETE FROM chat_messages").
		WithArgs("conv-1").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectExec("DELETE FROM chat_conversations").
		WithArgs("conv-1", "other-user").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectRollback()

	err := store.DeleteConversation(context.Background(), "t1", "conv-1", "other-user")
	if err == nil {
		t.Fatal("expected ErrNotFound for unowned conversation")
	}
}

func TestChatStore_AddMessage(t *testing.T) {
	store, mock := newChatStoreWithMock(t)
	defer mock.Close()

	now := time.Now()
	steps := json.RawMessage(`[{"type":"think","content":"hmm"}]`)
	msg := &domain.ChatMessage{
		ConversationID: "conv-1",
		Role:           "user",
		Content:        "hello",
		StepsJSON:      steps,
		IsError:        false,
	}

	expectTenantTx(mock)
	mock.ExpectExec("UPDATE chat_conversations").
		WithArgs("conv-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectQuery("INSERT INTO chat_messages").
		WithArgs("conv-1", "user", "hello", string(steps), false, "[]").
		WillReturnRows(pgxmock.NewRows([]string{"id", "created_at"}).AddRow("msg-uuid", now))
	mock.ExpectCommit()

	if err := store.AddMessage(context.Background(), "t1", msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.ID != "msg-uuid" {
		t.Errorf("want msg-uuid, got %s", msg.ID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet: %v", err)
	}
}

func TestChatStore_AddMessage_nilStepsDefaultsToEmpty(t *testing.T) {
	store, mock := newChatStoreWithMock(t)
	defer mock.Close()

	now := time.Now()
	msg := &domain.ChatMessage{
		ConversationID: "conv-1",
		Role:           "user",
		Content:        "hi",
		StepsJSON:      nil,
	}

	expectTenantTx(mock)
	mock.ExpectExec("UPDATE chat_conversations").
		WithArgs("conv-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectQuery("INSERT INTO chat_messages").
		WithArgs("conv-1", "user", "hi", "[]", false, "[]").
		WillReturnRows(pgxmock.NewRows([]string{"id", "created_at"}).AddRow("msg-2", now))
	mock.ExpectCommit()

	if err := store.AddMessage(context.Background(), "t1", msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet: %v", err)
	}
}

func TestChatStore_ListMessages(t *testing.T) {
	store, mock := newChatStoreWithMock(t)
	defer mock.Close()

	now := time.Now()
	expectTenantTx(mock)
	mock.ExpectQuery("SELECT m.id, m.conversation_id").
		WithArgs("conv-1", "user-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "conversation_id", "role", "content", "steps_json", "is_error", "created_at", "artifacts_json",
		}).
			AddRow("m1", "conv-1", "user", "hi", json.RawMessage("[]"), false, now, json.RawMessage("[]")).
			AddRow("m2", "conv-1", "assistant", "hello back", json.RawMessage("[]"), false, now, json.RawMessage("[]")))
	mock.ExpectCommit()

	msgs, err := store.ListMessages(context.Background(), "t1", "conv-1", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("want 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Errorf("unexpected roles: %s, %s", msgs[0].Role, msgs[1].Role)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet: %v", err)
	}
}

func TestChatStore_ArtifactRoundTrip(t *testing.T) {
	store, mock := newChatStoreWithMock(t)
	defer mock.Close()
	now := time.Now()
	artifacts := []domain.ExecutionArtifact{{Type: "diagnostic_report", ProfileVersion: "v1", DiagnosticReport: &domain.DiagnosticReport{Inferences: []string{}}}}
	raw, err := encodeExecutionArtifacts(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	msg := &domain.ChatMessage{ConversationID: "conv-1", Role: "assistant", Content: "ok", Artifacts: artifacts}
	expectTenantTx(mock)
	mock.ExpectExec("UPDATE chat_conversations").WithArgs("conv-1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectQuery("INSERT INTO chat_messages").WithArgs("conv-1", "assistant", "ok", "[]", false, string(raw)).
		WillReturnRows(pgxmock.NewRows([]string{"id", "created_at"}).AddRow("m1", now))
	mock.ExpectCommit()
	if err := store.AddMessage(context.Background(), "t1", msg); err != nil {
		t.Fatal(err)
	}

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT m.id, m.conversation_id").WithArgs("conv-1", "user-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "conversation_id", "role", "content", "steps_json", "is_error", "created_at", "artifacts_json"}).
			AddRow("m1", "conv-1", "assistant", "ok", json.RawMessage("[]"), false, now, raw))
	mock.ExpectCommit()
	got, err := store.ListMessages(context.Background(), "t1", "conv-1", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Artifacts) != 1 || got[0].Artifacts[0].ProfileVersion != "v1" {
		t.Fatalf("unexpected artifacts: %#v", got)
	}
}

func TestChatStore_HistoricalMessageHydratesEmptyArtifacts(t *testing.T) {
	store, mock := newChatStoreWithMock(t)
	defer mock.Close()
	now := time.Now()
	expectTenantTx(mock)
	mock.ExpectQuery("SELECT m.id, m.conversation_id").WithArgs("conv-1", "user-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "conversation_id", "role", "content", "steps_json", "is_error", "created_at", "artifacts_json"}).
			AddRow("m1", "conv-1", "assistant", "old", json.RawMessage("[]"), false, now, json.RawMessage("[]")))
	mock.ExpectCommit()
	got, err := store.ListMessages(context.Background(), "t1", "conv-1", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Artifacts == nil || len(got[0].Artifacts) != 0 {
		t.Fatalf("want non-nil empty artifacts: %#v", got[0].Artifacts)
	}
}

func TestChatStore_MalformedArtifactsReturnError(t *testing.T) {
	store, mock := newChatStoreWithMock(t)
	defer mock.Close()
	now := time.Now()
	expectTenantTx(mock)
	mock.ExpectQuery("SELECT m.id, m.conversation_id").WithArgs("conv-1", "user-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "conversation_id", "role", "content", "steps_json", "is_error", "created_at", "artifacts_json"}).
			AddRow("m1", "conv-1", "assistant", "bad", json.RawMessage("[]"), false, now, []byte(`{"broken":`)))
	mock.ExpectRollback()
	_, err := store.ListMessages(context.Background(), "t1", "conv-1", "user-1")
	if err == nil {
		t.Fatal("expected malformed artifact error")
	}
}

func TestDecodeExecutionArtifactsRejectsInvalidPersistedShapes(t *testing.T) {
	tests := map[string]string{
		"null":                 `null`,
		"unknown top field":    `[{"type":"citations","profileVersion":"v1","citations":[],"extra":1}]`,
		"unknown nested field": `[{"type":"diagnostic_report","profileVersion":"v1","diagnosticReport":{"facts":[],"inferences":[],"evidenceGaps":[],"recommendedActions":[],"citations":[],"steps":[],"extra":1}}]`,
		"empty artifact":       `[{}]`,
		"wrong discriminator":  `[{"type":"other","profileVersion":"v1"}]`,
		"exclusive fields":     `[{"type":"citations","profileVersion":"v1","citations":[],"diagnosticReport":{"facts":[],"inferences":[],"evidenceGaps":[],"recommendedActions":[],"citations":[],"steps":[]}}]`,
		"trailing json":        `[] {}`,
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeExecutionArtifacts([]byte(raw)); err == nil {
				t.Fatal("expected strict decode error")
			}
		})
	}
	got, err := decodeExecutionArtifacts([]byte(`[]`))
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("historical [] decode = %#v, %v", got, err)
	}
}

func TestChatStoreRejectsInvalidArtifactsBeforeTransaction(t *testing.T) {
	tests := map[string][]domain.ExecutionArtifact{
		"wrong type":       {{Type: "other", ProfileVersion: "v1"}},
		"exclusive":        {{Type: "citations", ProfileVersion: "v1", Citations: []domain.Citation{}, DiagnosticReport: &domain.DiagnosticReport{}}},
		"unsafe inference": {{Type: "diagnostic_report", ProfileVersion: "v1", DiagnosticReport: &domain.DiagnosticReport{Inferences: []string{"Authorization: Bearer secret"}}}},
		"unsafe action":    {{Type: "diagnostic_report", ProfileVersion: "v1", DiagnosticReport: &domain.DiagnosticReport{RecommendedActions: []string{"password=secret"}}}},
		"unsafe object":    {{Type: "diagnostic_report", ProfileVersion: "v1", DiagnosticReport: &domain.DiagnosticReport{Facts: []domain.DiagnosticFact{{Area: domain.DiagnosticAreaAgent, ObjectID: "password=secret", Statement: "ok", Source: "source"}}}}},
		"invalid area":     {{Type: "diagnostic_report", ProfileVersion: "v1", DiagnosticReport: &domain.DiagnosticReport{Facts: []domain.DiagnosticFact{{Area: "global", Statement: "ok", Source: "source"}}}}},
		"prose code":       {{Type: "diagnostic_report", ProfileVersion: "v1", DiagnosticReport: &domain.DiagnosticReport{Steps: []domain.DiagnosticStep{{Tool: "tool", Outcome: "maybe later", ErrorCode: "provider said no because prose"}}}}},
	}
	for name, artifacts := range tests {
		t.Run(name, func(t *testing.T) {
			store, mock := newChatStoreWithMock(t)
			defer mock.Close()
			err := store.AddMessage(context.Background(), "t1", &domain.ChatMessage{ConversationID: "c1", Role: "assistant", Content: "x", Artifacts: artifacts})
			if err == nil {
				t.Fatal("expected write invariant error")
			}
			if strings.Contains(err.Error(), "begin") {
				t.Fatalf("validation happened after transaction start: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("validation started transaction: %v", err)
			}
		})
	}
	store, mock := newChatStoreWithMock(t)
	defer mock.Close()
	huge := []domain.ExecutionArtifact{{Type: "citations", ProfileVersion: "v1", Citations: []domain.Citation{{DocumentID: "doc", Excerpt: strings.Repeat("x", 40*1024)}}}}
	if err := store.AddMessage(context.Background(), "t1", &domain.ChatMessage{ConversationID: "c1", Role: "assistant", Content: "x", Artifacts: huge}); err == nil || strings.Contains(err.Error(), "begin") {
		t.Fatalf("expected pre-transaction oversize rejection, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("oversize validation started transaction: %v", err)
	}
}

func TestChatStore_CleanupExpired(t *testing.T) {
	store, mock := newChatStoreWithMock(t)
	defer mock.Close()

	expectTenantTx(mock)
	mock.ExpectExec("DELETE FROM chat_conversations").
		WillReturnResult(pgxmock.NewResult("DELETE", 3))
	mock.ExpectCommit()

	if err := store.CleanupExpired(context.Background(), "t1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet: %v", err)
	}
}

func TestChatStore_InvalidTenantID(t *testing.T) {
	store, mock := newChatStoreWithMock(t)
	defer mock.Close()

	_, err := store.CreateConversation(context.Background(), "t1; DROP TABLE", "a", "u", "n")
	if err == nil {
		t.Fatal("expected error for invalid tenant_id")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet: %v", err)
	}
}
