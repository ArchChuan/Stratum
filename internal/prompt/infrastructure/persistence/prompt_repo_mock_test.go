package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/prompt/domain"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func newPromptMock(t *testing.T) pgxmock.PgxPoolIface {
	t.Helper()
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	return mock
}

var promptColumns = []string{
	"key", "tenant_id", "version", "content", "status", "content_hash", "created_by", "created_at",
}

func promptRow(key, content, status string, version int) []any {
	return []any{key, (*string)(nil), version, content, status, "hash-1", "user:1", time.Now()}
}

func TestPgPromptRepo_Insert_success(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgPromptRepo{pool: mock}
	tmpl := domain.PromptTemplate{
		Key: "k1", Version: 3, Content: "hello", Status: domain.PromptDraft,
		ContentHash: "h1", CreatedBy: "user:1",
	}

	mock.ExpectExec("INSERT INTO public.prompt_templates").
		WithArgs("k1", (*string)(nil), 3, "hello", "draft", "h1", "user:1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	require.NoError(t, repo.Insert(context.Background(), tmpl))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgPromptRepo_Insert_fails(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgPromptRepo{pool: mock}

	mock.ExpectExec("INSERT INTO public.prompt_templates").
		WillReturnError(pgx.ErrTxClosed)

	err := repo.Insert(context.Background(), domain.PromptTemplate{})
	require.ErrorContains(t, err, "prompt: insert")
}

func TestPgPromptRepo_GetByKey_globalTenant(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgPromptRepo{pool: mock}

	mock.ExpectQuery("FROM public.prompt_templates").
		WithArgs("k1", (*string)(nil)).
		WillReturnRows(pgxmock.NewRows(promptColumns).
			AddRow(promptRow("k1", "v3", "draft", 3)...).
			AddRow(promptRow("k1", "v2", "archived", 2)...))

	tmpls, err := repo.GetByKey(context.Background(), "k1", nil)
	require.NoError(t, err)
	require.Len(t, tmpls, 2)
	require.Equal(t, 3, tmpls[0].Version)
	require.Equal(t, domain.PromptDraft, tmpls[0].Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgPromptRepo_GetByKey_queryFails(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgPromptRepo{pool: mock}
	tenant := "t1"

	mock.ExpectQuery("FROM public.prompt_templates").
		WithArgs("k1", &tenant).
		WillReturnError(pgx.ErrTxClosed)

	_, err := repo.GetByKey(context.Background(), "k1", &tenant)
	require.ErrorContains(t, err, "prompt: get by key")
}

func TestPgPromptRepo_GetByKey_scanFails(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgPromptRepo{pool: mock}

	mock.ExpectQuery("FROM public.prompt_templates").
		WithArgs("k1", (*string)(nil)).
		WillReturnRows(pgxmock.NewRows(promptColumns).
			AddRow("k1", (*string)(nil), 3, "hello", 42, "h1", "user:1", time.Now()))

	_, err := repo.GetByKey(context.Background(), "k1", nil)
	require.ErrorContains(t, err, "prompt: scan")
}

func TestPgPromptRepo_GetVersion_found(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgPromptRepo{pool: mock}

	mock.ExpectQuery("FROM public.prompt_templates").
		WithArgs("k1", 3, (*string)(nil)).
		WillReturnRows(pgxmock.NewRows(promptColumns).AddRow(promptRow("k1", "v3", "published", 3)...))

	tmpl, err := repo.GetVersion(context.Background(), "k1", 3, nil)
	require.NoError(t, err)
	require.NotNil(t, tmpl)
	require.Equal(t, domain.PromptPublished, tmpl.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgPromptRepo_GetVersion_notFound(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgPromptRepo{pool: mock}

	mock.ExpectQuery("FROM public.prompt_templates").
		WithArgs("k1", 9, (*string)(nil)).
		WillReturnError(pgx.ErrNoRows)

	tmpl, err := repo.GetVersion(context.Background(), "k1", 9, nil)
	require.NoError(t, err)
	require.Nil(t, tmpl)
}

func TestPgPromptRepo_GetVersion_queryFails(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgPromptRepo{pool: mock}

	mock.ExpectQuery("FROM public.prompt_templates").
		WithArgs("k1", 3, (*string)(nil)).
		WillReturnError(pgx.ErrTxClosed)

	_, err := repo.GetVersion(context.Background(), "k1", 3, nil)
	require.ErrorContains(t, err, "prompt: get version")
}

func TestPgPromptRepo_GetLatestPublished_found(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgPromptRepo{pool: mock}

	mock.ExpectQuery("FROM public.prompt_templates").
		WithArgs("k1", (*string)(nil)).
		WillReturnRows(pgxmock.NewRows(promptColumns).AddRow(promptRow("k1", "v3", "published", 3)...))

	tmpl, err := repo.GetLatestPublished(context.Background(), "k1", nil)
	require.NoError(t, err)
	require.NotNil(t, tmpl)
	require.Equal(t, "v3", tmpl.Content)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgPromptRepo_GetLatestPublished_notFound(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgPromptRepo{pool: mock}

	mock.ExpectQuery("FROM public.prompt_templates").
		WithArgs("k1", (*string)(nil)).
		WillReturnError(pgx.ErrNoRows)

	tmpl, err := repo.GetLatestPublished(context.Background(), "k1", nil)
	require.NoError(t, err)
	require.Nil(t, tmpl)
}

func TestPgPromptRepo_UpdateStatus_success(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgPromptRepo{pool: mock}

	mock.ExpectExec("UPDATE public.prompt_templates").
		WithArgs("published", "k1", 3, (*string)(nil)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	require.NoError(t, repo.UpdateStatus(context.Background(), "k1", 3, nil, domain.PromptPublished))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgPromptRepo_UpdateStatus_fails(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgPromptRepo{pool: mock}

	mock.ExpectExec("UPDATE public.prompt_templates").
		WithArgs("archived", "k1", 3, (*string)(nil)).
		WillReturnError(pgx.ErrTxClosed)

	err := repo.UpdateStatus(context.Background(), "k1", 3, nil, domain.PromptArchived)
	require.ErrorContains(t, err, "prompt: update status")
}

func TestPgPromptRepo_GetByHash_found(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgPromptRepo{pool: mock}

	mock.ExpectQuery("FROM public.prompt_templates").
		WithArgs("h1").
		WillReturnRows(pgxmock.NewRows(promptColumns).AddRow(promptRow("k1", "v3", "published", 3)...))

	tmpl, err := repo.GetByHash(context.Background(), "h1")
	require.NoError(t, err)
	require.NotNil(t, tmpl)
	require.Equal(t, "hash-1", tmpl.ContentHash)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgPromptRepo_GetByHash_notFound(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgPromptRepo{pool: mock}

	mock.ExpectQuery("FROM public.prompt_templates").
		WithArgs("nope").
		WillReturnError(pgx.ErrNoRows)

	tmpl, err := repo.GetByHash(context.Background(), "nope")
	require.NoError(t, err)
	require.Nil(t, tmpl)
}

func TestPgBindingRepo_UpsertBinding_success(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgBindingRepo{pool: mock}
	b := domain.PromptBinding{Key: "k1", Scope: "tenant:t1", StableVersionID: "sv1", CanaryVersionID: "cv1", TrafficPercent: 20}

	mock.ExpectExec("INSERT INTO public.prompt_bindings").
		WithArgs("k1", "tenant:t1", "sv1", "cv1", 20).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	require.NoError(t, repo.UpsertBinding(context.Background(), b))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgBindingRepo_UpsertBinding_fails(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgBindingRepo{pool: mock}

	mock.ExpectExec("INSERT INTO public.prompt_bindings").
		WillReturnError(pgx.ErrTxClosed)

	err := repo.UpsertBinding(context.Background(), domain.PromptBinding{})
	require.ErrorContains(t, err, "prompt: upsert binding")
}

func TestPgBindingRepo_GetBinding_found(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgBindingRepo{pool: mock}

	mock.ExpectQuery("FROM public.prompt_bindings").
		WithArgs("k1", "tenant:t1").
		WillReturnRows(pgxmock.NewRows([]string{"key", "scope", "stable_version_id", "canary_version_id", "traffic_percent"}).
			AddRow("k1", "tenant:t1", "sv1", "cv1", 20))

	b, err := repo.GetBinding(context.Background(), "k1", "tenant:t1")
	require.NoError(t, err)
	require.NotNil(t, b)
	require.Equal(t, 20, b.TrafficPercent)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgBindingRepo_GetBinding_notFound(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgBindingRepo{pool: mock}

	mock.ExpectQuery("FROM public.prompt_bindings").
		WithArgs("k1", "tenant:t1").
		WillReturnError(pgx.ErrNoRows)

	b, err := repo.GetBinding(context.Background(), "k1", "tenant:t1")
	require.NoError(t, err)
	require.Nil(t, b)
}

func TestPgBindingRepo_ListBindings_multi(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgBindingRepo{pool: mock}

	mock.ExpectQuery("FROM public.prompt_bindings").
		WithArgs("agent:%").
		WillReturnRows(pgxmock.NewRows([]string{"key", "scope", "stable_version_id", "canary_version_id", "traffic_percent"}).
			AddRow("k1", "agent:a1", "sv1", "", 0).
			AddRow("k2", "agent:a2", "sv2", "cv2", 50))

	bindings, err := repo.ListBindings(context.Background(), "agent:")
	require.NoError(t, err)
	require.Len(t, bindings, 2)
	require.Equal(t, "agent:a2", bindings[1].Scope)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgBindingRepo_ListBindings_queryFails(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgBindingRepo{pool: mock}

	mock.ExpectQuery("FROM public.prompt_bindings").
		WithArgs("agent:%").
		WillReturnError(pgx.ErrTxClosed)

	_, err := repo.ListBindings(context.Background(), "agent:")
	require.ErrorContains(t, err, "prompt: list bindings")
}

func TestPgBindingRepo_ListBindings_scanFails(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgBindingRepo{pool: mock}

	mock.ExpectQuery("FROM public.prompt_bindings").
		WithArgs("agent:%").
		WillReturnRows(pgxmock.NewRows([]string{"key", "scope", "stable_version_id", "canary_version_id", "traffic_percent"}).
			AddRow("k1", "agent:a1", "sv1", "cv1", "not-a-number"))

	_, err := repo.ListBindings(context.Background(), "agent:")
	require.ErrorContains(t, err, "prompt: scan binding")
}

func TestPgBindingRepo_DeleteBinding_success(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgBindingRepo{pool: mock}

	mock.ExpectExec("DELETE FROM public.prompt_bindings").
		WithArgs("k1", "agent:a1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	require.NoError(t, repo.DeleteBinding(context.Background(), "k1", "agent:a1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgBindingRepo_DeleteBinding_fails(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgBindingRepo{pool: mock}

	mock.ExpectExec("DELETE FROM public.prompt_bindings").
		WithArgs("k1", "agent:a1").
		WillReturnError(pgx.ErrTxClosed)

	err := repo.DeleteBinding(context.Background(), "k1", "agent:a1")
	require.ErrorContains(t, err, "prompt: delete binding")
}

func TestPgBindingRepo_GetBinding_queryFails(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgBindingRepo{pool: mock}

	mock.ExpectQuery("FROM public.prompt_bindings").
		WithArgs("k1", "agent:a1").
		WillReturnError(pgx.ErrTxClosed)

	_, err := repo.GetBinding(context.Background(), "k1", "agent:a1")
	require.ErrorContains(t, err, "prompt: get binding")
}

func TestPgPromptRepo_GetVersion_tenantScoped(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgPromptRepo{pool: mock}
	tenant := "t1"

	mock.ExpectQuery("FROM public.prompt_templates").
		WithArgs("k1", 3, &tenant).
		WillReturnRows(pgxmock.NewRows(promptColumns).AddRow(promptRow("k1", "v3", "published", 3)...))

	tmpl, err := repo.GetVersion(context.Background(), "k1", 3, &tenant)
	require.NoError(t, err)
	require.NotNil(t, tmpl)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgPromptRepo_ListByKey_success(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgPromptRepo{pool: mock}

	mock.ExpectQuery("SELECT COUNT\\(DISTINCT key\\) FROM public.prompt_templates").
		WithArgs((*string)(nil)).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery("SELECT DISTINCT ON \\(key\\) key, tenant_id, version, content, status, content_hash, created_by, created_at").
		WithArgs((*string)(nil), 10, 0).
		WillReturnRows(pgxmock.NewRows(promptColumns).
			AddRow(promptRow("k1", "v3", "draft", 3)...).
			AddRow(promptRow("k2", "v2", "published", 2)...))

	tmpls, total, err := repo.ListByKey(context.Background(), nil, 10, 0)
	require.NoError(t, err)
	require.Len(t, tmpls, 2)
	require.Equal(t, 2, total)
	require.Equal(t, "k1", tmpls[0].Key)
	require.Equal(t, domain.PromptPublished, tmpls[1].Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgPromptRepo_ListByKey_countFails(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgPromptRepo{pool: mock}
	tenant := "t1"

	mock.ExpectQuery("SELECT COUNT\\(DISTINCT key\\)").
		WithArgs(&tenant).
		WillReturnError(pgx.ErrTxClosed)

	_, _, err := repo.ListByKey(context.Background(), &tenant, 10, 0)
	require.ErrorContains(t, err, "prompt: count keys")
}

func TestPgPromptRepo_ListByKey_queryFails(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgPromptRepo{pool: mock}

	mock.ExpectQuery("SELECT COUNT\\(DISTINCT key\\)").
		WithArgs((*string)(nil)).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT DISTINCT ON \\(key\\)").
		WithArgs((*string)(nil), 10, 0).
		WillReturnError(pgx.ErrTxClosed)

	_, _, err := repo.ListByKey(context.Background(), nil, 10, 0)
	require.ErrorContains(t, err, "prompt: list by key")
}

func TestPgPromptRepo_ListByKey_scanFails(t *testing.T) {
	mock := newPromptMock(t)
	repo := &PgPromptRepo{pool: mock}

	mock.ExpectQuery("SELECT COUNT\\(DISTINCT key\\)").
		WithArgs((*string)(nil)).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT DISTINCT ON \\(key\\)").
		WithArgs((*string)(nil), 10, 0).
		WillReturnRows(pgxmock.NewRows(promptColumns).
			AddRow("k1", (*string)(nil), 3, "hello", 42, "h1", "user:1", time.Now()))

	_, _, err := repo.ListByKey(context.Background(), nil, 10, 0)
	require.ErrorContains(t, err, "prompt: scan")
}
