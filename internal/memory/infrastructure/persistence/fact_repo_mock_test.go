package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func newFactMock(t *testing.T) pgxmock.PgxPoolIface {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	return mock
}

// pgxmock implements the package tenantPool (Begin), so tests inject it directly.
func newMockFactRepo(mock pgxmock.PgxPoolIface) *FactRepo { return &FactRepo{pool: mock} }

// anyArgs returns n AnyArg matchers for error-path expectations whose exact
// argument values are irrelevant to the branch under test.
func anyArgs(n int) []any {
	s := make([]any, n)
	for i := range s {
		s[i] = pgxmock.AnyArg()
	}
	return s
}

func pgErr23505() error { return &pgconn.PgError{Code: "23505"} }

func ts() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

// --- pure helpers ---

func TestValidateExtractedFactWrite(t *testing.T) {
	baseFact := func() *domain.MemoryFact {
		return &domain.MemoryFact{
			TenantID: "t1", UserID: "u1", AgentID: "ag1", Scope: domain.ScopeUser,
			Content: "c", Importance: 0.8,
		}
	}
	validWrite := func(f *domain.MemoryFact) *port.ExtractedFactWrite {
		return &port.ExtractedFactWrite{Fact: f, Identity: domain.FactSourceIdentity{MessageID: "m1", Ordinal: 0}, PayloadHash: "h"}
	}

	cases := []struct {
		name  string
		write *port.ExtractedFactWrite
		scope string
	}{
		{name: "valid user scope", write: validWrite(baseFact())},
		{name: "valid agent scope", write: validWrite(func() *domain.MemoryFact {
			f := baseFact()
			f.Scope = domain.ScopeAgent
			return f
		}())},
		{name: "empty tenantID", write: validWrite(baseFact()), scope: ""},
		{name: "nil write", write: nil},
		{name: "nil fact", write: &port.ExtractedFactWrite{Identity: domain.FactSourceIdentity{}, PayloadHash: "h"}},
		{name: "tenant mismatch", write: validWrite(func() *domain.MemoryFact {
			f := baseFact()
			f.TenantID = "other"
			return f
		}())},
		{name: "empty userID", write: validWrite(func() *domain.MemoryFact {
			f := baseFact()
			f.UserID = ""
			return f
		}())},
		{name: "empty messageID", write: validWrite(func() *domain.MemoryFact {
			return baseFact()
		}()), scope: "empty-msg"},
		{name: "negative ordinal", write: validWrite(func() *domain.MemoryFact {
			return baseFact()
		}()), scope: "neg-ordinal"},
		{name: "empty payload hash", write: &port.ExtractedFactWrite{Fact: baseFact(), Identity: domain.FactSourceIdentity{MessageID: "m1"}}},
		{name: "invalid scope", write: validWrite(func() *domain.MemoryFact {
			f := baseFact()
			f.Scope = "system"
			return f
		}())},
		{name: "agent scope missing agentID", write: validWrite(func() *domain.MemoryFact {
			f := baseFact()
			f.Scope = domain.ScopeAgent
			f.AgentID = ""
			return f
		}())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "empty tenantID" {
				tc.write.Fact.TenantID = ""
			}
			if tc.name == "empty messageID" {
				tc.write.Identity.MessageID = ""
			}
			if tc.name == "negative ordinal" {
				tc.write.Identity.Ordinal = -1
			}
			if tc.name == "valid user scope" || tc.name == "valid agent scope" {
				require.NoError(t, validateExtractedFactWrite("t1", tc.write))
				return
			}
			require.ErrorIs(t, validateExtractedFactWrite("t1", tc.write), domain.ErrInvalidFactSourceIdentity)
		})
	}
}

func TestNullableTaskID(t *testing.T) {
	require.Nil(t, nullableTaskID(0))
	require.Equal(t, int64(7), nullableTaskID(7))
}

func TestTranslatePgError(t *testing.T) {
	require.NoError(t, translatePgError(nil, "op"))
	require.ErrorContains(t, translatePgError(pgErr23505(), "create fact"), "duplicate entry")
	require.ErrorContains(t, translatePgError(&pgconn.PgError{Code: "23503"}, "create fact"), "foreign key violation")
	require.ErrorContains(t, translatePgError(&pgconn.PgError{Code: "23514"}, "create fact"), "constraint violation")
	require.ErrorContains(t, translatePgError(&pgconn.PgError{Code: "42601"}, "create fact"), "create fact")
	require.ErrorContains(t, translatePgError(errors.New("boom"), "op"), "op: boom")
}

func TestSupersedeScopeClauseAndQuery(t *testing.T) {
	userFilter := domain.ScopeFilter{UserID: "u1", IncludeUserScope: true, IncludeAgentScope: true}
	q, args := supersedeQuery(userFilter, "text", 0.6, 5)
	require.Contains(t, q, "similarity(content, $2) > $3")
	require.Equal(t, []any{"u1", "text", 0.6, 5}, args)
	require.Equal(t, "scope = 'user'", supersedeScopeClause(userFilter))

	agentFilter := domain.ScopeFilter{UserID: "u1", AgentID: "ag1", IncludeAgentScope: true}
	q2, args2 := supersedeQuery(agentFilter, "text", 0.6, 5)
	require.Contains(t, q2, "scope = 'agent' AND agent_id = $3")
	require.Contains(t, q2, "similarity(content, $2) > $4")
	require.Equal(t, []any{"u1", "text", "ag1", 0.6, 5}, args2)
	require.Equal(t, "scope = 'agent' AND agent_id = $3", supersedeScopeClause(agentFilter))
}

func TestFactRepo_execTenant_poolNil(t *testing.T) {
	repo := &FactRepo{pool: nil}
	err := repo.execTenant(context.Background(), "t1", func(context.Context, pgx.Tx) error { return nil })
	require.ErrorContains(t, err, "pool is nil")
}

func TestFactRepo_execTenant_emptyTenant(t *testing.T) {
	repo := newMockFactRepo(newFactMock(t))
	err := repo.execTenant(context.Background(), "", func(context.Context, pgx.Tx) error { return nil })
	require.ErrorContains(t, err, "tenant_id is empty")
}

// --- Create / GetByID / Update ---

var factColumns = []string{"id", "user_id", "agent_id", "scope", "conversation_id", "content", "importance",
	"status", "superseded_by", "access_count", "last_accessed_at",
	"created_at", "updated_at", "frecency_score", "category", "confidence", "source"}

func factRow(agent *string, conv *string, sup *string) []any {
	t := ts()
	return []any{"f1", "u1", agent, "user", conv, "content", 0.8,
		"active", sup, 1, t, t, t, 0.9, "other", 0.8, "llm_extraction"}
}

func TestFactRepo_Create_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	t0 := ts()
	ag, conv, sup := "ag1", "c1", "s1"
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("INSERT INTO memory_facts").
		WithArgs("f1", "u1", &ag, "user", &conv, "content", 0.8, "active", &sup, 1, t0, t0, t0, 0.9, "other", 0.8, "llm_extraction").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	f := &domain.MemoryFact{ID: "f1", TenantID: "t1", UserID: "u1", AgentID: "ag1", Scope: domain.ScopeUser,
		ConversationID: "c1", Content: "content", Importance: 0.8, Status: "active", SupersededBy: "s1",
		AccessCount: 1, LastAccessAt: t0, CreatedAt: t0, UpdatedAt: t0, FrecencyScore: 0.9,
		Category: "other", Confidence: 0.8, Source: "llm_extraction"}
	require.NoError(t, repo.Create(context.Background(), "t1", f))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_Create_nilPointers(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	t0 := ts()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("INSERT INTO memory_facts").
		WithArgs("f1", "u1", (*string)(nil), "agent", (*string)(nil), "c", 0.7, "active", (*string)(nil), 0, t0, t0, t0, 0.0, "other", 0.0, "").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	f := &domain.MemoryFact{ID: "f1", TenantID: "t1", UserID: "u1", Scope: domain.ScopeAgent,
		Content: "c", Importance: 0.7, Status: "active", Category: "other", CreatedAt: t0, UpdatedAt: t0, LastAccessAt: t0}
	require.NoError(t, repo.Create(context.Background(), "t1", f))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_Create_duplicate(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("INSERT INTO memory_facts").
		WithArgs(anyArgs(17)...).
		WillReturnError(pgErr23505())
	mock.ExpectRollback()

	err := repo.Create(context.Background(), "t1", &domain.MemoryFact{ID: "f1", UserID: "u1", Scope: domain.ScopeUser})
	require.ErrorContains(t, err, "duplicate entry")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_GetByID_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	ag, conv, sup := "ag1", "c1", "s1"
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_facts WHERE id = \\$1").
		WithArgs("f1").
		WillReturnRows(pgxmock.NewRows(factColumns).AddRow(factRow(&ag, &conv, &sup)...))
	mock.ExpectCommit()

	f, err := repo.GetByID(context.Background(), "t1", "f1")
	require.NoError(t, err)
	require.Equal(t, "ag1", f.AgentID)
	require.Equal(t, "c1", f.ConversationID)
	require.Equal(t, "s1", f.SupersededBy)
	require.Equal(t, domain.ScopeUser, f.Scope)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_GetByID_notFound(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_facts WHERE id = \\$1").
		WithArgs("nope").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	f, err := repo.GetByID(context.Background(), "t1", "nope")
	require.Nil(t, f)
	require.ErrorIs(t, err, domain.ErrFactNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_GetByID_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_facts WHERE id = \\$1").
		WithArgs("f1").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.GetByID(context.Background(), "t1", "f1")
	require.ErrorContains(t, err, "get fact by id")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_Update_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("UPDATE memory_facts SET").
		WithArgs("f1", "new", 0.9, "active", (*string)(nil), 2, pgxmock.AnyArg(), pgxmock.AnyArg(), 0.95, "", 0.0).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	f := &domain.MemoryFact{ID: "f1", Content: "new", Importance: 0.9, Status: "active", AccessCount: 2, FrecencyScore: 0.95}
	require.NoError(t, repo.Update(context.Background(), "t1", f))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_Update_notFound(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("UPDATE memory_facts SET").
		WithArgs("nope", "new", 0.9, "active", (*string)(nil), 0, pgxmock.AnyArg(), pgxmock.AnyArg(), 0.0, "", 0.0).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()

	err := repo.Update(context.Background(), "t1", &domain.MemoryFact{ID: "nope", Content: "new", Importance: 0.9, Status: "active"})
	require.ErrorIs(t, err, domain.ErrFactNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_Update_execFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("UPDATE memory_facts SET").
		WithArgs(anyArgs(11)...).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.Update(context.Background(), "t1", &domain.MemoryFact{ID: "f1"})
	require.ErrorContains(t, err, "update fact")
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- ListActive / SearchByContent ---

func TestFactRepo_ListActive_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	ag := "ag1"
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_facts").
		WithArgs("u1", true, "ag1", true, 10).
		WillReturnRows(pgxmock.NewRows(factColumns).
			AddRow(factRow(&ag, nil, nil)...).
			AddRow(factRow(nil, nil, nil)...))
	mock.ExpectCommit()

	filter := domain.BuildScopeFilter("t1", "u1", "ag1", "user")
	facts, err := repo.ListActive(context.Background(), "t1", filter, 10)
	require.NoError(t, err)
	require.Len(t, facts, 2)
	require.Equal(t, "ag1", facts[0].AgentID)
	require.Equal(t, "", facts[1].AgentID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_ListActive_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_facts").
		WithArgs("u1", true, "ag1", true, 10).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	filter := domain.BuildScopeFilter("t1", "u1", "ag1", "user")
	_, err := repo.ListActive(context.Background(), "t1", filter, 10)
	require.ErrorContains(t, err, "list active facts")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_SearchByContent_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("content ILIKE").
		WithArgs("u1", "%dark%", true, "", true, 5).
		WillReturnRows(pgxmock.NewRows(factColumns).AddRow(factRow(nil, nil, nil)...))
	mock.ExpectCommit()

	filter := domain.BuildScopeFilter("t1", "u1", "", "user")
	facts, err := repo.SearchByContent(context.Background(), "t1", filter, "dark", 5)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	require.Equal(t, "content", facts[0].Content)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_SearchByContent_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("content ILIKE").
		WithArgs(anyArgs(6)...).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	filter := domain.BuildScopeFilter("t1", "u1", "", "user")
	_, err := repo.SearchByContent(context.Background(), "t1", filter, "dark", 5)
	require.ErrorContains(t, err, "search facts by content")
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- FindSupersedeCandidates ---

var candidateColumns = []string{"id", "user_id", "agent_id", "scope", "content", "importance",
	"status", "superseded_by", "access_count", "last_accessed_at", "created_at", "updated_at",
	"category", "confidence", "sim"}

func TestFactRepo_FindSupersedeCandidates_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	t0 := ts()
	ag, sup := "ag1", "s1"
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("similarity\\(content").
		WithArgs("u1", "text", "ag1", 0.6, 5).
		WillReturnRows(pgxmock.NewRows(candidateColumns).
			AddRow("f1", "u1", &ag, "agent", "text", 0.9, "active", &sup, 1, t0, t0, t0, "preference", 0.8, 0.87).
			AddRow("f2", "u1", nil, "agent", "text2", 0.7, "active", nil, 0, t0, t0, t0, "other", 0.7, 0.81))
	mock.ExpectCommit()

	filter := domain.ScopeFilter{UserID: "u1", AgentID: "ag1", IncludeAgentScope: true}
	cands, err := repo.FindSupersedeCandidates(context.Background(), "t1", filter, "text", 0.6, 5)
	require.NoError(t, err)
	require.Len(t, cands, 2)
	require.Equal(t, "ag1", cands[0].Fact.AgentID)
	require.InDelta(t, 0.87, cands[0].Similarity, 0.001)
	require.Equal(t, "s1", cands[0].Fact.SupersededBy)
	require.Equal(t, "preference", cands[0].Fact.Category)
	require.InDelta(t, 0.8, cands[0].Fact.Confidence, 0.001)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_FindSupersedeCandidates_userScope(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	t0 := ts()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("similarity\\(content").
		WithArgs("u1", "text", 0.6, 5).
		WillReturnRows(pgxmock.NewRows(candidateColumns).AddRow("f1", "u1", nil, "user", "text", 0.9, "active", nil, 1, t0, t0, t0, "other", 0.8, 0.5))
	mock.ExpectCommit()

	filter := domain.ScopeFilter{UserID: "u1", IncludeUserScope: true}
	cands, err := repo.FindSupersedeCandidates(context.Background(), "t1", filter, "text", 0.6, 5)
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_FindSupersedeCandidates_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("similarity\\(content").
		WithArgs(anyArgs(4)...).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	filter := domain.ScopeFilter{UserID: "u1", IncludeUserScope: true}
	_, err := repo.FindSupersedeCandidates(context.Background(), "t1", filter, "text", 0.6, 5)
	require.ErrorContains(t, err, "find supersede candidates")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_FindSupersedeCandidates_scanFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	// int in a *string column -> per-row Scan error.
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("similarity\\(content").
		WithArgs(anyArgs(4)...).
		WillReturnRows(pgxmock.NewRows(candidateColumns).AddRow("f1", 42, nil, "user", "text", 0.9, "active", nil, 1, ts(), ts(), ts(), "other", 0.8, 0.5))
	mock.ExpectRollback()

	filter := domain.ScopeFilter{UserID: "u1", IncludeUserScope: true}
	_, err := repo.FindSupersedeCandidates(context.Background(), "t1", filter, "text", 0.6, 5)
	require.ErrorContains(t, err, "scan supersede candidate")
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- CountByUser / Delete / DeleteAll / Purge ---

func TestFactRepo_CountByUser(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("COUNT\\(\\*\\) FROM memory_facts").
		WithArgs("u1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(3))
	mock.ExpectCommit()

	count, err := repo.CountByUser(context.Background(), "t1", "u1")
	require.NoError(t, err)
	require.Equal(t, 3, count)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_CountByUser_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("COUNT\\(\\*\\) FROM memory_facts").
		WithArgs(anyArgs(1)...).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.CountByUser(context.Background(), "t1", "u1")
	require.ErrorContains(t, err, "count facts by user")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_ListUserFacts_userScopedNewestFirst(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_facts").
		WithArgs("u1", 5, 10).
		WillReturnRows(pgxmock.NewRows(factColumns).
			AddRow(factRow(nil, nil, nil)...).
			AddRow(factRow(nil, nil, nil)...))
	mock.ExpectCommit()

	facts, err := repo.ListUserFacts(context.Background(), "t1", "u1", 5, 10)
	require.NoError(t, err)
	require.Len(t, facts, 2)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_ListUserFacts_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_facts").
		WithArgs("u1", 20, 0).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.ListUserFacts(context.Background(), "t1", "u1", 20, 0)
	require.ErrorContains(t, err, "list user facts")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestFactRepo_CountAll counts every memory_facts row regardless of status —
// the snapshot denominator for migration progress, which must not drift when
// facts are written concurrently mid-migration.
func TestFactRepo_CountAll(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM memory_facts").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(12))
	mock.ExpectCommit()

	count, err := repo.CountAll(context.Background(), "t1")
	require.NoError(t, err)
	require.Equal(t, 12, count)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_CountAll_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM memory_facts").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.CountAll(context.Background(), "t1")
	require.ErrorContains(t, err, "count all facts")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestFactRepo_ListAllFacts returns facts of any status ordered by created_at, id
// — the stable cursor for migration backfill resume (offset = progress).
func TestFactRepo_ListAllFacts(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("ORDER BY created_at, id").
		WithArgs(5, 10).
		WillReturnRows(pgxmock.NewRows(factColumns).
			AddRow(factRow(nil, nil, nil)...).
			AddRow(factRow(nil, nil, nil)...))
	mock.ExpectCommit()

	facts, err := repo.ListAllFacts(context.Background(), "t1", 5, 10)
	require.NoError(t, err)
	require.Len(t, facts, 2)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_ListAllFacts_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("ORDER BY created_at, id").
		WithArgs(20, 0).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.ListAllFacts(context.Background(), "t1", 20, 0)
	require.ErrorContains(t, err, "list all facts")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_Delete(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM memory_facts WHERE id = \\$1").
		WithArgs("f1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Delete(context.Background(), "t1", "f1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_Delete_execFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM memory_facts WHERE id = \\$1").
		WithArgs(anyArgs(1)...).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.Delete(context.Background(), "t1", "f1")
	require.ErrorContains(t, err, "delete fact")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_DeleteAllByUser_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("DELETE FROM memory_facts WHERE user_id = \\$1 RETURNING id").
		WithArgs("u1").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("f1").AddRow("f2"))
	mock.ExpectCommit()

	ids, err := repo.DeleteAllByUser(context.Background(), "t1", "u1")
	require.NoError(t, err)
	require.Equal(t, []string{"f1", "f2"}, ids)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_DeleteAllByUser_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("DELETE FROM memory_facts WHERE user_id").
		WithArgs(anyArgs(1)...).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.DeleteAllByUser(context.Background(), "t1", "u1")
	require.ErrorContains(t, err, "delete all by user")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_DeleteAllByUser_scanFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("DELETE FROM memory_facts WHERE user_id").
		WithArgs(anyArgs(1)...).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(42))
	mock.ExpectRollback()

	_, err := repo.DeleteAllByUser(context.Background(), "t1", "u1")
	require.ErrorContains(t, err, "scan deleted fact id")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_DeleteAllByAgent_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("DELETE FROM memory_facts WHERE agent_id = \\$1 AND scope = 'agent'").
		WithArgs("ag1").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("f1"))
	mock.ExpectCommit()

	ids, err := repo.DeleteAllByAgent(context.Background(), "t1", "ag1")
	require.NoError(t, err)
	require.Equal(t, []string{"f1"}, ids)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_DeleteAllByAgent_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("DELETE FROM memory_facts WHERE agent_id").
		WithArgs(anyArgs(1)...).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.DeleteAllByAgent(context.Background(), "t1", "ag1")
	require.ErrorContains(t, err, "delete all by agent")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_PurgeSuperseded_limitZero(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)
	// No SQL expected: non-positive limit short-circuits.
	ids, err := repo.PurgeSuperseded(context.Background(), "t1", ts(), 0)
	require.NoError(t, err)
	require.Nil(t, ids)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_PurgeSuperseded_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("DELETE FROM memory_facts").
		WithArgs(pgxmock.AnyArg(), 100).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("f1").AddRow("f2"))
	mock.ExpectCommit()

	ids, err := repo.PurgeSuperseded(context.Background(), "t1", ts(), 100)
	require.NoError(t, err)
	require.Equal(t, []string{"f1", "f2"}, ids)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_PurgeSuperseded_execFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("DELETE FROM memory_facts").
		WithArgs(anyArgs(2)...).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.PurgeSuperseded(context.Background(), "t1", ts(), 100)
	require.ErrorContains(t, err, "purge superseded facts")
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- CreateExtracted (replay-safe provenance) ---

var extractedFactColumns = []string{"id", "user_id", "agent_id", "scope", "conversation_id", "content", "importance",
	"status", "superseded_by", "access_count", "last_accessed_at", "created_at", "updated_at", "frecency_score",
	"category", "confidence", "source", "source_message_id", "source_task_id", "source_ordinal", "source_payload_hash"}

func extractedWrite() *port.ExtractedFactWrite {
	t0 := ts()
	return &port.ExtractedFactWrite{
		Fact: &domain.MemoryFact{ID: "f1", TenantID: "t1", UserID: "u1", AgentID: "ag1", Scope: domain.ScopeAgent,
			Content: "c", Importance: 0.8, Status: "active", AccessCount: 0, LastAccessAt: t0, CreatedAt: t0, UpdatedAt: t0,
			Category: "other", Confidence: 0.8, Source: "llm_extraction"},
		Identity:    domain.FactSourceIdentity{MessageID: "m1", TaskID: 3, Ordinal: 0},
		PayloadHash: "hash1",
		EntityNames: []string{"alice"},
	}
}

func TestFactRepo_CreateExtracted_newFact(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("INSERT INTO memory_facts").
		WithArgs("f1", "u1", &extractedWrite().Fact.AgentID, "agent", (*string)(nil), "c", 0.8, "active",
			0, pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), 0.0, "other", 0.8, "llm_extraction",
			"m1", int64(3), 0, "hash1").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("f1"))
	// Entity lookup misses -> create a fresh entity.
	mock.ExpectQuery("similarity\\(name").
		WithArgs("u1", "alice", 0.6, "agent", "ag1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("INSERT INTO memory_entities").
		WithArgs("u1", &extractedWrite().Fact.AgentID, "agent", "alice").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("e1"))
	mock.ExpectCommit()

	fact, created, err := repo.CreateExtracted(context.Background(), "t1", extractedWrite())
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, []string{"e1"}, fact.EntityIDs)
	require.Equal(t, "hash1", fact.SourcePayloadHash)
	require.Equal(t, int64(3), fact.SourceIdentity.TaskID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_CreateExtracted_existingEntity(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("INSERT INTO memory_facts").
		WithArgs(anyArgs(20)...).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("f1"))
	// Entity already exists -> bump its counters.
	mock.ExpectQuery("similarity\\(name").
		WithArgs("u1", "alice", 0.6, "agent", "ag1").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("e9"))
	mock.ExpectQuery("UPDATE memory_entities SET fact_count").
		WithArgs("e9").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("e9"))
	mock.ExpectCommit()

	fact, created, err := repo.CreateExtracted(context.Background(), "t1", extractedWrite())
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, []string{"e9"}, fact.EntityIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_CreateExtracted_replaySameHash(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	t0 := ts()
	// ON CONFLICT DO NOTHING -> no row, pgx returns ErrNoRows.
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("INSERT INTO memory_facts").
		WithArgs(anyArgs(20)...).
		WillReturnError(pgx.ErrNoRows)
	// Same identity replayed with the same payload hash -> return the persisted fact.
	mock.ExpectQuery("FOR UPDATE").
		WithArgs("u1", "m1", 0, "agent", "ag1").
		WillReturnRows(pgxmock.NewRows(extractedFactColumns).AddRow(
			"f1", "u1", "ag1", "agent", "", "c", 0.8, "active", "", 1, t0, t0, t0, 0.0,
			"other", 0.8, "llm_extraction", "m1", int64(3), 0, "hash1"))
	mock.ExpectCommit()

	fact, created, err := repo.CreateExtracted(context.Background(), "t1", extractedWrite())
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, "f1", fact.ID)
	require.Equal(t, []string{"alice"}, fact.EntityNames)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_CreateExtracted_payloadConflict(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("INSERT INTO memory_facts").
		WithArgs(anyArgs(20)...).
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("FOR UPDATE").
		WithArgs("u1", "m1", 0, "agent", "ag1").
		WillReturnRows(pgxmock.NewRows(extractedFactColumns).AddRow(
			"f1", "u1", "ag1", "agent", "", "c", 0.8, "active", "", 1, ts(), ts(), ts(), 0.0,
			"other", 0.8, "llm_extraction", "m1", int64(3), 0, "DIFFERENT"))
	mock.ExpectRollback()

	_, _, err := repo.CreateExtracted(context.Background(), "t1", extractedWrite())
	require.ErrorIs(t, err, domain.ErrFactSourceConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_CreateExtracted_invalidWrite(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)
	// Validation fails before any SQL: fail closed on incomplete provenance.
	_, _, err := repo.CreateExtracted(context.Background(), "", extractedWrite())
	require.ErrorIs(t, err, domain.ErrInvalidFactSourceIdentity)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_CreateExtracted_insertFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("INSERT INTO memory_facts").
		WithArgs(anyArgs(20)...).
		WillReturnError(pgErr23505())
	mock.ExpectRollback()

	_, _, err := repo.CreateExtracted(context.Background(), "t1", extractedWrite())
	require.ErrorContains(t, err, "insert extracted fact")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_CreateExtracted_entityLookupFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("INSERT INTO memory_facts").
		WithArgs(anyArgs(20)...).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("f1"))
	// Non-ErrNoRows lookup failure aborts the tx.
	mock.ExpectQuery("similarity\\(name").
		WithArgs(anyArgs(5)...).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, _, err := repo.CreateExtracted(context.Background(), "t1", extractedWrite())
	require.ErrorContains(t, err, "find extracted fact entity")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_CreateExtracted_userScopeValidation(t *testing.T) {
	// ScopeUser without agent is valid (agent provenance optional).
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)

	w := extractedWrite()
	w.Fact.AgentID = ""
	w.Fact.Scope = domain.ScopeUser
	w.Fact.TenantID = "t1"

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("INSERT INTO memory_facts").
		WithArgs("f1", "u1", (*string)(nil), "user", (*string)(nil), "c", 0.8, "active",
			0, pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), 0.0, "other", 0.8, "llm_extraction",
			"m1", int64(3), 0, "hash1").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("f1"))
	mock.ExpectQuery("similarity\\(name").
		WithArgs("u1", "alice", 0.6, "user").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("e1"))
	mock.ExpectQuery("UPDATE memory_entities SET fact_count").
		WithArgs("e1").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("e1"))
	mock.ExpectCommit()

	fact, created, err := repo.CreateExtracted(context.Background(), "t1", w)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, []string{"e1"}, fact.EntityIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- ListUserFactsFiltered / CountUserFactsFiltered (management page list) ---

func TestFactRepo_ListUserFactsFiltered(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)
	ts := ts()
	rows := pgxmock.NewRows([]string{"id", "user_id", "agent_id", "scope", "conversation_id", "content", "importance",
		"status", "superseded_by", "access_count", "last_accessed_at",
		"created_at", "updated_at", "frecency_score", "category", "confidence", "source"}).
		AddRow("fact-1", "user-1", nil, "user", nil, "I prefer dark mode", 0.8,
			"active", nil, 1, ts, ts, ts, 0.5, "preference", 0.9, "explicit_user")

	importanceMin, importanceMax := 0.5, 0.9
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("SELECT id, user_id, agent_id").
		WithArgs("user-1", "%dark%", "preference", importanceMin, importanceMax, 10, 0).
		WillReturnRows(rows)
	mock.ExpectCommit()

	got, err := repo.ListUserFactsFiltered(context.Background(), "tenant-1", "user-1",
		domain.FactListFilter{Query: "dark", ImportanceMin: &importanceMin, ImportanceMax: &importanceMax, Category: "preference"},
		10, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "fact-1", got[0].ID)
	require.Equal(t, "preference", got[0].Category)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFactRepo_CountUserFactsFiltered(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("SELECT count").
		WithArgs("user-1", "%dark%").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(3))
	mock.ExpectCommit()

	total, err := repo.CountUserFactsFiltered(context.Background(), "tenant-1", "user-1",
		domain.FactListFilter{Query: "dark"})
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestFactRepo_Update_SupersedePreservesCategory guards the regression where
// superseding a fact silently wiped its category/confidence: supersedeQuery
// originally SELECTed only 12 columns, so the candidate's Fact carried zero
// values, and Update wrote those zeros back. The chain below mirrors
// supersedeCandidate (extraction.go): find candidates -> MarkSuperseded -> Update.
func TestFactRepo_Update_SupersedePreservesCategory(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockFactRepo(mock)
	t0 := ts()
	ag, sup := "ag1", "f-new"

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("similarity\\(content").
		WithArgs("u1", "text", "ag1", 0.6, 5).
		WillReturnRows(pgxmock.NewRows(candidateColumns).
			AddRow("f1", "u1", &ag, "agent", "text", 0.9, "active", nil, 1, t0, t0, t0, "preference", 0.8, 0.87))
	mock.ExpectCommit()

	filter := domain.ScopeFilter{UserID: "u1", AgentID: "ag1", IncludeAgentScope: true}
	cands, err := repo.FindSupersedeCandidates(context.Background(), "t1", filter, "text", 0.6, 5)
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.Equal(t, "preference", cands[0].Fact.Category)
	require.InDelta(t, 0.8, cands[0].Fact.Confidence, 0.001)

	require.NoError(t, cands[0].Fact.MarkSuperseded(sup))
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("UPDATE memory_facts SET").
		WithArgs("f1", "text", 0.9, "superseded", &sup, 1, pgxmock.AnyArg(), pgxmock.AnyArg(), 0.0, "preference", 0.8).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Update(context.Background(), "t1", cands[0].Fact))
	require.NoError(t, mock.ExpectationsWereMet())
}
