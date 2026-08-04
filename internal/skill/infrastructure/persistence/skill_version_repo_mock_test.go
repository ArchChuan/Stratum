package persistence

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func newSkillMock(t *testing.T) pgxmock.PgxPoolIface {
	t.Helper()
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	return mock
}

func skillTenantCtx() context.Context {
	return tenantdb.WithTenant(context.Background(),
		&tenantdb.TenantContext{TenantID: "t1", UserID: "u1", Role: tenantdb.RoleTenantAdmin})
}

func beginTenantTx(t *testing.T, mock pgxmock.PgxPoolIface) {
	t.Helper()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").
		WillReturnResult(pgxmock.NewResult("SET", 0))
}

func newSkillRepo(mock pgxmock.PgxPoolIface) *PgSkillRevisionRepo {
	return &PgSkillRevisionRepo{pool: mock}
}

func skillRevisionRow() []any {
	return []any{
		"r-1", "s-1", "p-1", 2, "draft", "evolution", "h-1",
		[]byte(`{"gen":1}`), []byte(`{"goal":"g","whenToUse":""}`),
		[]byte(`{"name":"ac","description":"","inputSchema":null,"outputSchema":null,"confirmed":false}`),
		"do it", []byte(`{"mcpToolIds":["t1"]}`), []byte(`{"ok":true}`),
	}
}

var revisionCols = []string{
	"id", "skill_id", "parent_revision_id", "revision_no", "status", "source", "content_hash",
	"generation_metadata", "capability", "activation_contract", "instructions", "requirements", "publish_checks",
}

func TestPgSkillRevisionRepo_InsertSkillWithDraft_success(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectExec("INSERT INTO skills").
		WithArgs("s-1", "name", "desc", "r-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO skill_revisions").
		WithArgs("r-1", "s-1", "p-1", 2, "draft", "evolution", "h-1",
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			"do it", pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	err := repo.InsertSkillWithDraft(skillTenantCtx(), port.SkillProductRow{ID: "s-1", Name: "name", Description: "desc"},
		testSkillRevision("r-1"))
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_InsertSkillWithDraft_manualSourceFallback(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectExec("INSERT INTO skills").
		WithArgs("s-1", "n", "d", "r-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	// Empty Source and empty ParentRevisionID map to 'manual' and NULL semantics.
	mock.ExpectExec("INSERT INTO skill_revisions").
		WithArgs("r-1", "s-1", "", 0, "draft", "manual", "h-1",
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			"do it", pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	rev := testSkillRevision("r-1")
	rev.Source = ""
	rev.ParentRevisionID = ""
	rev.RevisionNo = 0
	err := repo.InsertSkillWithDraft(skillTenantCtx(), port.SkillProductRow{ID: "s-1", Name: "n", Description: "d"}, rev)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_InsertSkillWithDraft_skillInsertFails(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectExec("INSERT INTO skills").
		WithArgs("s-1", "", "", "r-1").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.InsertSkillWithDraft(skillTenantCtx(), port.SkillProductRow{ID: "s-1"}, testSkillRevision("r-1"))
	require.ErrorIs(t, err, pgx.ErrTxClosed)
}

func TestPgSkillRevisionRepo_InsertSkillWithDraft_revisionInsertFails(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectExec("INSERT INTO skills").
		WithArgs("s-1", "", "", "r-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO skill_revisions").
		WithArgs("r-1", "s-1", "p-1", 2, "draft", "evolution", "h-1",
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			"do it", pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.InsertSkillWithDraft(skillTenantCtx(), port.SkillProductRow{ID: "s-1"}, testSkillRevision("r-1"))
	require.ErrorIs(t, err, pgx.ErrTxClosed)
}

func TestPgSkillRevisionRepo_GetSkill_found(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("FROM skills WHERE id").
		WithArgs("s-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "description", "status", "active_revision_id", "draft_revision_id"}).
			AddRow("s-1", "n", "d", "published", "ar-1", ""))
	mock.ExpectCommit()

	row, found, err := repo.GetSkill(skillTenantCtx(), "s-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "ar-1", row.ActiveRevisionID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_GetSkill_notFound(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("FROM skills WHERE id").
		WithArgs("missing").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectCommit()

	_, found, err := repo.GetSkill(skillTenantCtx(), "missing")
	require.NoError(t, err)
	require.False(t, found)
}

func TestPgSkillRevisionRepo_GetSkill_queryFails(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("FROM skills WHERE id").
		WithArgs("s-1").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, _, err := repo.GetSkill(skillTenantCtx(), "s-1")
	require.ErrorIs(t, err, pgx.ErrTxClosed)
}

func TestPgSkillRevisionRepo_ListSkills_multi(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("FROM skills ORDER BY name").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "description", "status", "active_revision_id", "draft_revision_id"}).
			AddRow("s-1", "a", "d1", "draft", "", "dr-1").
			AddRow("s-2", "b", "d2", "published", "ar-2", ""))
	mock.ExpectCommit()

	rows, err := repo.ListSkills(skillTenantCtx())
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "dr-1", rows[0].DraftRevisionID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_ListSkills_scanFails(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("FROM skills ORDER BY name").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "description", "status", "active_revision_id", "draft_revision_id"}).
			AddRow("s-1", 42, "d1", "draft", "", ""))
	mock.ExpectRollback()

	_, err := repo.ListSkills(skillTenantCtx())
	require.Error(t, err)
}

func TestPgSkillRevisionRepo_DeleteSkill_success(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectExec("DELETE FROM skills WHERE id").
		WithArgs("s-1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.DeleteSkill(skillTenantCtx(), "s-1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_DeleteSkill_notFound(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectExec("DELETE FROM skills WHERE id").
		WithArgs("s-1").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectRollback()

	require.ErrorIs(t, repo.DeleteSkill(skillTenantCtx(), "s-1"), domain.ErrSkillNotFound)
}

func TestPgSkillRevisionRepo_GetDraftRevision_found(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("FROM skill_revisions WHERE skill_id=\\$1 AND status='draft'").
		WithArgs("s-1").
		WillReturnRows(pgxmock.NewRows(revisionCols).AddRow(skillRevisionRow()...))
	mock.ExpectCommit()

	rev, found, err := repo.GetDraftRevision(skillTenantCtx(), "s-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "r-1", rev.ID)
	require.Equal(t, domain.VersionStatusDraft, rev.Status)
	require.Equal(t, "g", rev.Capability.Goal, "JSON columns must unmarshal")
	require.Equal(t, []string{"t1"}, rev.Requirements.MCPToolIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_GetDraftRevision_notFound(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("FROM skill_revisions WHERE skill_id=\\$1 AND status='draft'").
		WithArgs("s-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectCommit()

	_, found, err := repo.GetDraftRevision(skillTenantCtx(), "s-1")
	require.NoError(t, err)
	require.False(t, found)
}

func TestPgSkillRevisionRepo_GetActiveRevision_queryFails(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("JOIN skills s ON s.active_revision_id").
		WithArgs("s-1").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, _, err := repo.GetActiveRevision(skillTenantCtx(), "s-1")
	require.ErrorIs(t, err, pgx.ErrTxClosed)
}

func TestPgSkillRevisionRepo_GetRevision_found(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("FROM skill_revisions WHERE skill_id=\\$1 AND id=\\$2").
		WithArgs("s-1", "r-9").
		WillReturnRows(pgxmock.NewRows(revisionCols).AddRow(skillRevisionRow()...))
	mock.ExpectCommit()

	rev, found, err := repo.GetRevision(skillTenantCtx(), "s-1", "r-9")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "r-1", rev.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_InsertCandidate_success(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectExec("INSERT INTO skill_revisions").
		WithArgs("c-1", "s-1", "p-1", 2, "candidate", "evolution", "h-1",
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			"do it", pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	cand := testSkillRevision("c-1")
	cand.Status = domain.VersionStatusCandidate
	require.NoError(t, repo.InsertCandidate(skillTenantCtx(), cand))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_UpdateDraftCapability_success(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("UPDATE skill_revisions SET capability=\\$2, content_hash=\\$3").
		WithArgs("s-1", `{"goal":"g","whenToUse":""}`, "h-2").
		WillReturnRows(pgxmock.NewRows(revisionCols).AddRow(skillRevisionRow()...))
	mock.ExpectCommit()

	// Second transaction: refresh skill description.
	beginTenantTx(t, mock)
	mock.ExpectExec("UPDATE skills SET description=\\$2").
		WithArgs("s-1", "g").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	rev, err := repo.UpdateDraftCapability(skillTenantCtx(), "s-1", domain.Capability{Goal: "g"}, "h-2")
	require.NoError(t, err)
	require.Equal(t, "r-1", rev.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_UpdateDraftCapability_queryFails(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("UPDATE skill_revisions SET capability=\\$2, content_hash=\\$3").
		WithArgs("s-1", `{"goal":"g","whenToUse":""}`, "h-2").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.UpdateDraftCapability(skillTenantCtx(), "s-1", domain.Capability{Goal: "g"}, "h-2")
	require.ErrorIs(t, err, pgx.ErrTxClosed)
}

func TestPgSkillRevisionRepo_UpdateDraftActivation_success(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("UPDATE skill_revisions SET activation_contract=\\$2, content_hash=\\$3").
		WithArgs("s-1", `{"name":"ac","description":"","inputSchema":null,"outputSchema":null,"confirmed":false}`, "h-3").
		WillReturnRows(pgxmock.NewRows(revisionCols).AddRow(skillRevisionRow()...))
	mock.ExpectCommit()

	rev, err := repo.UpdateDraftActivation(skillTenantCtx(), "s-1", domain.ActivationContract{Name: "ac"}, "h-3")
	require.NoError(t, err)
	require.Equal(t, "r-1", rev.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_UpdateDraftInstructions_success(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("UPDATE skill_revisions SET instructions=\\$2, requirements=\\$3, content_hash=\\$4").
		WithArgs("s-1", "do it", `{"mcpToolIds":["t1"]}`, "h-4").
		WillReturnRows(pgxmock.NewRows(revisionCols).AddRow(skillRevisionRow()...))
	mock.ExpectCommit()

	rev, err := repo.UpdateDraftInstructions(skillTenantCtx(), "s-1", "do it",
		domain.Requirements{MCPToolIDs: []string{"t1"}}, "h-4")
	require.NoError(t, err)
	require.Equal(t, "r-1", rev.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_UpdateDraftBundle_success(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("UPDATE skill_revisions SET capability=\\$3::jsonb").
		WithArgs("s-1", "h-1", `{"goal":"g","whenToUse":""}`, `{"name":"ac","description":"","inputSchema":null,"outputSchema":null,"confirmed":false}`,
			"do it", `{"mcpToolIds":["t1"]}`, "h-1").
		WillReturnRows(pgxmock.NewRows(revisionCols).AddRow(skillRevisionRow()...))
	mock.ExpectExec("UPDATE skills SET name=\\$2, description=\\$3").
		WithArgs("s-1", "new-name", "new-desc").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	draft := testSkillRevision("r-1")
	rev, err := repo.UpdateDraftBundle(skillTenantCtx(), "s-1", "h-1",
		port.SkillProductRow{ID: "s-1", Name: "new-name", Description: "new-desc"}, draft)
	require.NoError(t, err)
	require.Equal(t, "r-1", rev.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_UpdateDraftBundle_stale(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("UPDATE skill_revisions SET capability=\\$3::jsonb").
		WithArgs("s-1", "stale-hash", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(pgx.ErrNoRows)
	// Draft still exists but hash changed → ErrSkillDraftStale.
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("s-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()

	_, err := repo.UpdateDraftBundle(skillTenantCtx(), "s-1", "stale-hash",
		port.SkillProductRow{ID: "s-1"}, testSkillRevision("r-1"))
	require.ErrorIs(t, err, domain.ErrSkillDraftStale)
}

func TestPgSkillRevisionRepo_UpdateDraftBundle_noDraft(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("UPDATE skill_revisions SET capability=\\$3::jsonb").
		WithArgs("s-1", "h-1", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("s-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectRollback()

	_, err := repo.UpdateDraftBundle(skillTenantCtx(), "s-1", "h-1",
		port.SkillProductRow{ID: "s-1"}, testSkillRevision("r-1"))
	require.ErrorIs(t, err, domain.ErrSkillNotFound)
}

func TestPgSkillRevisionRepo_NextRevisionNo(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("SELECT COALESCE\\(MAX\\(revision_no\\), 0\\) \\+ 1").
		WithArgs("s-1").
		WillReturnRows(pgxmock.NewRows([]string{"next"}).AddRow(5))
	mock.ExpectCommit()

	next, err := repo.NextRevisionNo(skillTenantCtx(), "s-1")
	require.NoError(t, err)
	require.Equal(t, 5, next)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_PublishDraft_success(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectExec("SET status='deprecated'").
		WithArgs("s-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectQuery("SET status='published', revision_no=\\$3, publish_checks=\\$4").
		WithArgs("r-1", "s-1", 3, `{"ok":true}`).
		WillReturnRows(pgxmock.NewRows(revisionCols).AddRow(skillRevisionRow()...))
	mock.ExpectExec("SET status='published', active_revision_id=\\$2").
		WithArgs("s-1", "r-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	rev, err := repo.PublishDraft(skillTenantCtx(), "s-1", "r-1", 3, map[string]any{"ok": true})
	require.NoError(t, err)
	require.Equal(t, "r-1", rev.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_PublishDraft_updateFails(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectExec("SET status='deprecated'").
		WithArgs("s-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectQuery("SET status='published', revision_no=\\$3, publish_checks=\\$4").
		WithArgs("r-1", "s-1", 3, `{"ok":true}`).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.PublishDraft(skillTenantCtx(), "s-1", "r-1", 3, map[string]any{"ok": true})
	require.ErrorIs(t, err, pgx.ErrTxClosed)
}

func TestPgSkillRevisionRepo_missingTenantFailsClosed(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)

	// No TenantContext in ctx → fail closed before any SQL is issued.
	_, found, err := repo.GetSkill(context.Background(), "s-1")
	require.False(t, found)
	require.ErrorContains(t, err, "missing tenant context")
}

func TestPgSkillRevisionRepo_emptyTenantFailsClosed(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)

	ctx := tenantdb.WithTenant(context.Background(), &tenantdb.TenantContext{TenantID: ""})
	_, _, err := repo.GetActiveRevision(ctx, "s-1")
	require.ErrorContains(t, err, "missing tenant context")
}

func testSkillRevision(id string) domain.SkillRevision {
	return domain.SkillRevision{
		ID:                 id,
		SkillID:            "s-1",
		ParentRevisionID:   "p-1",
		RevisionNo:         2,
		Status:             domain.VersionStatusDraft,
		Source:             "evolution",
		ContentHash:        "h-1",
		GenerationMetadata: map[string]any{"gen": 1},
		Capability:         domain.Capability{Goal: "g"},
		ActivationContract: domain.ActivationContract{Name: "ac"},
		Instructions:       "do it",
		Requirements:       domain.Requirements{MCPToolIDs: []string{"t1"}},
		PublishChecks:      map[string]any{"ok": true},
	}
}
