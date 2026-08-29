package persistence

import (
	"context"
	"testing"
	"time"

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

// skillRevisionRow returns a 15-column revision row (revisionColumns): id,
// skill_id, parent_revision_id, revision_no, status, source, content_hash,
// generation_metadata, name, description, instructions, publish_checks,
// created_by, created_at, published_at.
func skillRevisionRow() []any {
	createdAt := time.Unix(100, 0)
	return []any{
		"r-1", "s-1", "p-1", 2, "draft", "evolution", "h-1",
		[]byte(`{"gen":1}`),
		"skill name", "skill description", "do it", []byte(`{"ok":true}`),
		"", createdAt, nil,
	}
}

var revisionCols = []string{
	"id", "skill_id", "parent_revision_id", "revision_no", "status", "source", "content_hash",
	"generation_metadata", "name", "description", "instructions", "publish_checks",
	"created_by", "created_at", "published_at",
}

// listRevisionRow returns a 16-column row: skillRevisionRow() plus is_current
// (ListRevisions' skills.active_revision_id join projection).
func listRevisionRow(current bool) []any {
	return append(skillRevisionRow(), current)
}

var listRevisionCols = append(append([]string{}, revisionCols...), "is_current")

// expectInsertSkillRevision pins the 13-argument skill_revisions INSERT used by
// both InsertSkill and InsertCandidate.
func expectInsertSkillRevision(mock pgxmock.PgxPoolIface, id, status, source string) {
	mock.ExpectExec("INSERT INTO skill_revisions").
		WithArgs(id, "s-1", "p-1", 2, status, source, "h-1",
			pgxmock.AnyArg(), "skill name", "skill description",
			"do it", pgxmock.AnyArg(), "").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
}

// expectInsertDraftRevision pins the skill_revisions INSERT for a draft row in
// insertSkillRevision's parameter order: empty parent, revision_no=0 (NULLIF'd
// to NULL), per-draft content fields and the caller's created_by. The existing
// expectInsertSkillRevision pins testSkillRevision-derived values, which differ
// from a draft's, so drafts use this dedicated helper.
func expectInsertDraftRevision(mock pgxmock.PgxPoolIface, rev domain.SkillRevision) {
	mock.ExpectExec("INSERT INTO skill_revisions").
		WithArgs(rev.ID, rev.SkillID, rev.ParentRevisionID, rev.RevisionNo, string(rev.Status), rev.Source, rev.ContentHash,
			pgxmock.AnyArg(), rev.Name, rev.Description, rev.Instructions, pgxmock.AnyArg(), rev.CreatedBy).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
}

func TestPgSkillRevisionRepo_InsertSkill_success(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectExec("INSERT INTO skills").
		WithArgs("s-1", "name", "desc", "r-1", "").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	expectInsertSkillRevision(mock, "r-1", "published", "evolution")
	mock.ExpectCommit()

	rev := testSkillRevision("r-1")
	rev.Status = domain.VersionStatusPublished
	err := repo.InsertSkill(skillTenantCtx(), port.SkillProductRow{ID: "s-1", Name: "name", Description: "desc"},
		rev, nil, nil)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_InsertSkill_manualSourceFallback(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectExec("INSERT INTO skills").
		WithArgs("s-1", "n", "d", "r-1", "").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	// Empty Source and empty ParentRevisionID map to 'manual' and NULL semantics.
	mock.ExpectExec("INSERT INTO skill_revisions").
		WithArgs("r-1", "s-1", "", 0, "published", "manual", "h-1",
			pgxmock.AnyArg(), "skill name", "skill description",
			"do it", pgxmock.AnyArg(), "").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	rev := testSkillRevision("r-1")
	rev.Status = domain.VersionStatusPublished
	rev.Source = ""
	rev.ParentRevisionID = ""
	rev.RevisionNo = 0
	err := repo.InsertSkill(skillTenantCtx(), port.SkillProductRow{ID: "s-1", Name: "n", Description: "d"}, rev, nil, nil)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_InsertSkill_skillInsertFails(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectExec("INSERT INTO skills").
		WithArgs("s-1", "", "", "r-1", "").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	rev := testSkillRevision("r-1")
	rev.Status = domain.VersionStatusPublished
	err := repo.InsertSkill(skillTenantCtx(), port.SkillProductRow{ID: "s-1"}, rev, nil, nil)
	require.ErrorIs(t, err, pgx.ErrTxClosed)
}

func TestPgSkillRevisionRepo_InsertSkill_revisionInsertFails(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectExec("INSERT INTO skills").
		WithArgs("s-1", "", "", "r-1", "").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO skill_revisions").
		WithArgs("r-1", "s-1", "p-1", 2, "published", "evolution", "h-1",
			pgxmock.AnyArg(), "skill name", "skill description",
			"do it", pgxmock.AnyArg(), "").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	rev := testSkillRevision("r-1")
	rev.Status = domain.VersionStatusPublished
	err := repo.InsertSkill(skillTenantCtx(), port.SkillProductRow{ID: "s-1"}, rev, nil, nil)
	require.ErrorIs(t, err, pgx.ErrTxClosed)
}

func TestPgSkillRevisionRepo_GetSkill_found(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("FROM skills WHERE id").
		WithArgs("s-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "description", "status", "active_revision_id", "draft_revision_id", "created_by"}).
			AddRow("s-1", "n", "d", "published", "ar-1", "", ""))
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
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "description", "status", "active_revision_id", "draft_revision_id", "created_by"}).
			AddRow("s-1", "a", "d1", "published", "ar-1", "", "").
			AddRow("s-2", "b", "d2", "published", "ar-2", "", ""))
	mock.ExpectCommit()

	rows, err := repo.ListSkills(skillTenantCtx())
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "ar-1", rows[0].ActiveRevisionID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_ListSkills_scanFails(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("FROM skills ORDER BY name").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "description", "status", "active_revision_id", "draft_revision_id", "created_by"}).
			AddRow("s-1", 42, "d1", "published", "", "", ""))
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
	mock.ExpectExec("DELETE FROM resource_editors WHERE resource_kind=\\$1 AND resource_id=\\$2").
		WithArgs("skill", "s-1").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectCommit()

	require.NoError(t, repo.DeleteSkill(skillTenantCtx(), "s-1", nil))
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

	require.ErrorIs(t, repo.DeleteSkill(skillTenantCtx(), "s-1", nil), domain.ErrSkillNotFound)
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

func TestPgSkillRevisionRepo_GetActiveRevision_found(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("JOIN skills s ON s.active_revision_id").
		WithArgs("s-1").
		WillReturnRows(pgxmock.NewRows(revisionCols).AddRow(skillRevisionRow()...))
	mock.ExpectCommit()

	rev, found, err := repo.GetActiveRevision(skillTenantCtx(), "s-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "r-1", rev.ID)
	require.NoError(t, mock.ExpectationsWereMet())
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

	expectInsertSkillRevision(mock, "c-1", "candidate", "evolution")
	mock.ExpectCommit()

	cand := testSkillRevision("c-1")
	cand.Status = domain.VersionStatusCandidate
	require.NoError(t, repo.InsertCandidate(skillTenantCtx(), cand, nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

// expectSaveRevisionBody pins the shared SaveRevision write sequence: EXISTS
// guard → (optional) content-hash concurrency check → deprecate current →
// insert new revision → flip the skills row to the new active revision.
func expectSaveRevisionBody(mock pgxmock.PgxPoolIface, withBaseline bool, activeHash string) {
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("s-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	if withBaseline {
		mock.ExpectQuery("COALESCE\\(r.content_hash").
			WithArgs("s-1").
			WillReturnRows(pgxmock.NewRows([]string{"hash"}).AddRow(activeHash))
	}
	mock.ExpectExec("UPDATE skill_revisions SET status='deprecated'").
		WithArgs("s-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	expectInsertSkillRevision(mock, "r-1", "published", "evolution")
	mock.ExpectExec("UPDATE skills SET name=\\$2, description=\\$3").
		WithArgs("s-1", "new-name", "new-desc", "r-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
}

func TestPgSkillRevisionRepo_SaveRevision_success(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	expectSaveRevisionBody(mock, true, "h-1")
	mock.ExpectCommit()

	rev := testSkillRevision("r-1")
	rev.Status = domain.VersionStatusPublished
	saved, err := repo.SaveRevision(skillTenantCtx(), "s-1", "h-1",
		port.SkillProductRow{ID: "s-1", Name: "new-name", Description: "new-desc"}, rev, nil, "")
	require.NoError(t, err)
	require.Equal(t, "r-1", saved.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_SaveRevision_noBaseline(t *testing.T) {
	// 空 expectedContentHash(系统直写/存量未发布首版保存)跳过乐观并发校验。
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	expectSaveRevisionBody(mock, false, "")
	mock.ExpectCommit()

	rev := testSkillRevision("r-1")
	rev.Status = domain.VersionStatusPublished
	_, err := repo.SaveRevision(skillTenantCtx(), "s-1", "",
		port.SkillProductRow{ID: "s-1", Name: "new-name", Description: "new-desc"}, rev, nil, "")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_SaveRevision_stale(t *testing.T) {
	// 期望 hash 与当前生效版本不一致 → ErrSkillDraftStale(409)。
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("s-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("COALESCE\\(r.content_hash").
		WithArgs("s-1").
		WillReturnRows(pgxmock.NewRows([]string{"hash"}).AddRow("other-hash"))
	mock.ExpectRollback()

	_, err := repo.SaveRevision(skillTenantCtx(), "s-1", "h-1",
		port.SkillProductRow{ID: "s-1"}, testSkillRevision("r-1"), nil, "")
	require.ErrorIs(t, err, domain.ErrSkillDraftStale)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_SaveRevision_skillMissing(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("ghost").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectRollback()

	_, err := repo.SaveRevision(skillTenantCtx(), "ghost", "",
		port.SkillProductRow{ID: "ghost"}, testSkillRevision("r-1"), nil, "")
	require.ErrorIs(t, err, domain.ErrSkillNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_SaveRevision_missingTenantFailsClosed(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)

	// No TenantContext → fail closed before any SQL.
	_, err := repo.SaveRevision(context.Background(), "s-1", "",
		port.SkillProductRow{ID: "s-1"}, testSkillRevision("r-1"), nil, "")
	require.ErrorContains(t, err, "missing tenant context")
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

func TestPgSkillRevisionRepo_RollbackRevision_success(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectExec("UPDATE skill_revisions SET status='deprecated'").
		WithArgs("s-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE skill_revisions SET status='published', published_at=NOW").
		WithArgs("r-1", "s-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE skills SET active_revision_id=\\$2").
		WithArgs("s-1", "r-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.RollbackRevision(skillTenantCtx(), "s-1", "r-1", "", nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_RollbackRevision_targetNotDeprecated(t *testing.T) {
	// 目标不是该 skill 的 deprecated 历史版本 → ErrSkillNotFound。
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectExec("UPDATE skill_revisions SET status='deprecated'").
		WithArgs("s-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE skill_revisions SET status='published', published_at=NOW").
		WithArgs("r-1", "s-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()

	require.ErrorIs(t, repo.RollbackRevision(skillTenantCtx(), "s-1", "r-1", "", nil), domain.ErrSkillNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_ListRevisions_success(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("s-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("AS is_current").
		WithArgs("s-1").
		WillReturnRows(pgxmock.NewRows(listRevisionCols).
			AddRow(listRevisionRow(true)...).
			AddRow(listRevisionRow(false)...))
	mock.ExpectCommit()

	rows, found, err := repo.ListRevisions(skillTenantCtx(), "s-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, rows, 2)
	require.True(t, rows[0].IsCurrent)
	require.False(t, rows[1].IsCurrent)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_ListRevisions_missingSkill(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("ghost").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectCommit()

	rows, found, err := repo.ListRevisions(skillTenantCtx(), "ghost")
	require.NoError(t, err)
	require.False(t, found)
	require.Empty(t, rows)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_ListRevisions_queryFails(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("s-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("AS is_current").
		WithArgs("s-1").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, _, err := repo.ListRevisions(skillTenantCtx(), "s-1")
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
		Name:               "skill name",
		Description:        "skill description",
		Instructions:       "do it",
		PublishChecks:      map[string]any{"ok": true},
	}
}

func TestPgSkillRevisionRepo_SaveDraft_overwritesExisting(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("s-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	// UPDATE 命中既有草稿行(覆盖更新,id 不变)。
	mock.ExpectExec("UPDATE skill_revisions SET name=\\$2, description=\\$3, instructions=\\$4").
		WithArgs("s-1", "draft-name", "draft-desc", "draft-ins", "h-draft", "u1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	draft := domain.SkillRevision{ID: "dr-1", SkillID: "s-1", Status: domain.VersionStatusDraft, Source: "manual",
		Name: "draft-name", Description: "draft-desc", Instructions: "draft-ins", ContentHash: "h-draft", CreatedBy: "u1"}
	require.NoError(t, repo.SaveDraft(skillTenantCtx(), "s-1", "", draft, "", nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_SaveDraft_insertsNew(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	draft := domain.SkillRevision{ID: "dr-1", SkillID: "s-1", Status: domain.VersionStatusDraft, Source: "manual",
		Name: "draft-name", Description: "draft-desc", Instructions: "draft-ins", ContentHash: "h-draft", CreatedBy: "u1"}

	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("s-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec("UPDATE skill_revisions SET name=\\$2, description=\\$3, instructions=\\$4").
		WithArgs("s-1", "draft-name", "draft-desc", "draft-ins", "h-draft", "u1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	expectInsertDraftRevision(mock, draft)
	mock.ExpectExec("UPDATE skills SET draft_revision_id=\\$2").
		WithArgs("s-1", "dr-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.SaveDraft(skillTenantCtx(), "s-1", "", draft, "", nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_SaveDraft_stale(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("s-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("COALESCE\\(r.content_hash").
		WithArgs("s-1").
		WillReturnRows(pgxmock.NewRows([]string{"hash"}).AddRow("other-hash"))
	mock.ExpectRollback()

	draft := domain.SkillRevision{ID: "dr-1", SkillID: "s-1", Status: domain.VersionStatusDraft}
	require.ErrorIs(t, repo.SaveDraft(skillTenantCtx(), "s-1", "h-1", draft, "", nil), domain.ErrSkillDraftStale)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_PublishDraft_success(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("s-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	// demote 旧 active → promote 草稿 → 重指指针。顺序必须 demote 在前。
	mock.ExpectExec("UPDATE skill_revisions SET status='deprecated'").
		WithArgs("s-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE skill_revisions SET status='published', revision_no=\\$3, published_at=NOW").
		WithArgs("s-1", "dr-1", 2, "r-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE skills SET active_revision_id=\\$2, draft_revision_id=NULL").
		WithArgs("s-1", "dr-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.PublishDraft(skillTenantCtx(), "s-1", "dr-1", "r-1", 2, "", "", nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_PublishDraft_noDraft(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("s-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec("UPDATE skill_revisions SET status='deprecated'").
		WithArgs("s-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectExec("UPDATE skill_revisions SET status='published', revision_no=\\$3, published_at=NOW").
		WithArgs("s-1", "dr-1", 2, "r-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()

	require.ErrorIs(t, repo.PublishDraft(skillTenantCtx(), "s-1", "dr-1", "r-1", 2, "", "", nil), domain.ErrSkillDraftNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_DiscardDraft_success(t *testing.T) {
	mock := newSkillMock(t)
	repo := newSkillRepo(mock)
	beginTenantTx(t, mock)

	mock.ExpectExec("DELETE FROM skill_revisions WHERE skill_id=\\$1 AND status='draft'").
		WithArgs("s-1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec("UPDATE skills SET draft_revision_id=NULL").
		WithArgs("s-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.DiscardDraft(skillTenantCtx(), "s-1", "", nil))
	require.NoError(t, mock.ExpectationsWereMet())
}
