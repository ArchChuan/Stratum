package infrastructure

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func newMockModelRepo(mock pgxmock.PgxPoolIface) *PgModelRepo {
	return &PgModelRepo{pool: mock}
}

func modelFixture() *domain.Model {
	return &domain.Model{
		ID: "m1", TenantID: "t1", ProviderID: "p1", Name: "gpt-4", DisplayName: "GPT-4",
		Capabilities:  []domain.ModelCapability{domain.CapChat, domain.CapVision},
		ContextWindow: 8192, MaxTokens: 4096, InputPrice: 10.0, OutputPrice: 30.0,
		Recommended: true, Enabled: true,
	}
}

var modelColumns = []string{"id", "tenant_id", "provider_id", "name", "display_name", "capabilities",
	"context_window", "max_tokens", "input_price", "output_price", "recommended", "default_embedding",
	"enabled", "provider_managed", "created_at", "updated_at"}

func modelRow(m *domain.Model) []any {
	now := time.Now()
	return []any{m.ID, m.TenantID, m.ProviderID, m.Name, m.DisplayName,
		[]string{"chat", "vision"}, m.ContextWindow, m.MaxTokens, m.InputPrice, m.OutputPrice,
		m.Recommended, m.DefaultEmbedding, m.Enabled, m.ProviderManaged, now, now}
}

func TestModelRepo_beginFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectBegin().WillReturnError(pgx.ErrTxClosed)

	err := repo.Create(context.Background(), "t1", modelFixture())
	require.ErrorContains(t, err, "begin tx")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Create_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	m := modelFixture()
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("INSERT INTO models").
		WithArgs(m.ID, "t1", m.ProviderID, m.Name, m.DisplayName, []string{"chat", "vision"},
			m.ContextWindow, m.MaxTokens, m.InputPrice, m.OutputPrice, m.Recommended, m.Enabled, m.ProviderManaged).
		WillReturnRows(pgxmock.NewRows([]string{"created_at", "updated_at"}).AddRow(now, now))
	mock.ExpectCommit()

	require.NoError(t, repo.Create(context.Background(), "t1", m))
	require.Equal(t, now, m.CreatedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Create_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	m := modelFixture()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("INSERT INTO models").
		WithArgs(anyArgs(13)...).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.Create(context.Background(), "t1", m)
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Get_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	m := modelFixture()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM models WHERE id=\\$1").
		WithArgs("m1").
		WillReturnRows(pgxmock.NewRows(modelColumns).AddRow(modelRow(m)...))
	mock.ExpectCommit()

	got, err := repo.Get(context.Background(), "t1", "m1")
	require.NoError(t, err)
	require.Equal(t, "gpt-4", got.Name)
	require.Equal(t, []domain.ModelCapability{domain.CapChat, domain.CapVision}, got.Capabilities)
	require.Equal(t, m.DefaultEmbedding, got.DefaultEmbedding)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Get_notFound(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM models WHERE id=\\$1").
		WithArgs("nope").WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	_, err := repo.Get(context.Background(), "t1", "nope")
	require.ErrorContains(t, err, "get model")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_List_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM models WHERE tenant_id=\\$1").
		WithArgs("t1").
		WillReturnRows(pgxmock.NewRows(modelColumns).
			AddRow(modelRow(modelFixture())...))
	mock.ExpectCommit()

	models, err := repo.List(context.Background(), "t1", port.ModelFilter{})
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "gpt-4", models[0].Name)
	require.Equal(t, modelFixture().DefaultEmbedding, models[0].DefaultEmbedding)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_List_empty(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM models WHERE tenant_id=\\$1").
		WithArgs("t1").
		WillReturnRows(pgxmock.NewRows(modelColumns))
	mock.ExpectCommit()

	models, err := repo.List(context.Background(), "t1", port.ModelFilter{})
	require.NoError(t, err)
	require.Empty(t, models) // nil -> []domain.Model{}, never nil
	require.NotNil(t, models)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_List_withFilters(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	enabled := true
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("AND provider_id=\\$2 AND enabled=\\$3 AND \\$4 = ANY\\(capabilities\\)").
		WithArgs("t1", "p1", true, "vision").
		WillReturnRows(pgxmock.NewRows(modelColumns).
			AddRow(modelRow(modelFixture())...))
	mock.ExpectCommit()

	models, err := repo.List(context.Background(), "t1", port.ModelFilter{ProviderID: "p1", Enabled: &enabled, Capability: domain.CapVision})
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_List_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM models WHERE tenant_id=\\$1").
		WithArgs("t1").WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.List(context.Background(), "t1", port.ModelFilter{})
	require.ErrorContains(t, err, "list models")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_List_scanFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	bad := modelFixture()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM models WHERE tenant_id=\\$1").
		WithArgs("t1").
		WillReturnRows(pgxmock.NewRows(modelColumns).
			AddRow(42, bad.TenantID, bad.ProviderID, bad.Name, bad.DisplayName, []string{"chat"},
				bad.ContextWindow, bad.MaxTokens, bad.InputPrice, bad.OutputPrice,
				bad.Recommended, bad.DefaultEmbedding, bad.Enabled, bad.ProviderManaged, time.Now(), time.Now()))
	mock.ExpectRollback()

	_, err := repo.List(context.Background(), "t1", port.ModelFilter{})
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Update_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	m := modelFixture()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec(`UPDATE models SET display_name=\$1, capabilities=\$2, context_window=\$3, max_tokens=\$4,\s+input_price=\$5, output_price=\$6, recommended=\$7, enabled=\$8, updated_at=now\(\),\s+default_embedding = default_embedding AND \$8 AND 'embedding' = ANY\(\$2\)\s+WHERE id=\$9 AND tenant_id=\$10`).
		WithArgs(m.DisplayName, []string{"chat", "vision"}, m.ContextWindow, m.MaxTokens,
			m.InputPrice, m.OutputPrice, m.Recommended, m.Enabled, m.ID, "t1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Update(context.Background(), "t1", m))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Update_notFound(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec(`UPDATE models SET display_name=\$1, capabilities=\$2, context_window=\$3, max_tokens=\$4,\s+input_price=\$5, output_price=\$6, recommended=\$7, enabled=\$8, updated_at=now\(\),\s+default_embedding = default_embedding AND \$8 AND 'embedding' = ANY\(\$2\)\s+WHERE id=\$9 AND tenant_id=\$10`).
		WithArgs(anyArgs(10)...).WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()

	err := repo.Update(context.Background(), "t1", modelFixture())
	require.ErrorContains(t, err, "model not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Update_execFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec(`UPDATE models SET display_name=\$1, capabilities=\$2, context_window=\$3, max_tokens=\$4,\s+input_price=\$5, output_price=\$6, recommended=\$7, enabled=\$8, updated_at=now\(\),\s+default_embedding = default_embedding AND \$8 AND 'embedding' = ANY\(\$2\)\s+WHERE id=\$9 AND tenant_id=\$10`).
		WithArgs(anyArgs(10)...).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.Update(context.Background(), "t1", modelFixture())
	require.ErrorContains(t, err, "update model")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Delete_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM models").
		WithArgs("m1", "t1").WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Delete(context.Background(), "t1", "m1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Delete_providerManaged(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM models").
		WithArgs("m1", "t1").WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectRollback()

	err := repo.Delete(context.Background(), "t1", "m1")
	require.ErrorContains(t, err, "provider-managed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Toggle_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec(`UPDATE models SET enabled=\$1, updated_at=now\(\),\s+default_embedding = default_embedding AND \$1 AND 'embedding' = ANY\(capabilities\)\s+WHERE id=\$2 AND tenant_id=\$3`).
		WithArgs(false, "m1", "t1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Toggle(context.Background(), "t1", "m1", false))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Toggle_notFound(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec(`UPDATE models SET enabled=\$1, updated_at=now\(\),\s+default_embedding = default_embedding AND \$1 AND 'embedding' = ANY\(capabilities\)\s+WHERE id=\$2 AND tenant_id=\$3`).
		WithArgs(anyArgs(3)...).WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()

	err := repo.Toggle(context.Background(), "t1", "m1", true)
	require.ErrorContains(t, err, "model not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgModelRepoSetDefaultEmbedding(t *testing.T) {
	tests := []struct {
		name       string
		enabled    bool
		expectErr  bool
		expectSQLs []string // 事务内期望执行顺序；expectErr 时末条 UPDATE 返回 RowsAffected=0
		expectArgs [][]any  // 与 expectSQLs 一一对应的 UPDATE 参数
	}{
		{
			name:    "sets default clears others atomically in one transaction",
			enabled: true,
			expectSQLs: []string{
				`UPDATE models SET default_embedding=false WHERE tenant_id=\$1 AND id<>\$2`,
				`UPDATE models SET default_embedding=true WHERE id=\$1 AND tenant_id=\$2 AND enabled AND 'embedding' = ANY\(capabilities\)`,
			},
			expectArgs: [][]any{
				{"tenant-a", "model-1"},
				{"model-1", "tenant-a"},
			},
		},
		{
			name:    "clears default for target only when disabled",
			enabled: false,
			expectSQLs: []string{
				`UPDATE models SET default_embedding=false WHERE id=\$1 AND tenant_id=\$2`,
			},
			expectArgs: [][]any{
				{"model-1", "tenant-a"},
			},
		},
		{
			name:      "fails closed when target not enabled embedding model",
			enabled:   true,
			expectErr: true,
			expectSQLs: []string{
				`UPDATE models SET default_embedding=false WHERE tenant_id=\$1 AND id<>\$2`,
				`UPDATE models SET default_embedding=true WHERE id=\$1 AND tenant_id=\$2 AND enabled AND 'embedding' = ANY\(capabilities\)`,
			},
			expectArgs: [][]any{
				{"tenant-a", "model-1"},
				{"model-1", "tenant-a"},
			},
		},
		{
			name:      "fails when clearing unknown model",
			enabled:   false,
			expectErr: true,
			expectSQLs: []string{
				`UPDATE models SET default_embedding=false WHERE id=\$1 AND tenant_id=\$2`,
			},
			expectArgs: [][]any{
				{"model-1", "tenant-a"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := newFactMock(t)
			repo := newMockModelRepo(mock)

			mock.ExpectBegin()
			mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
			for i, sql := range tc.expectSQLs {
				rows := int64(1)
				if tc.expectErr && i == len(tc.expectSQLs)-1 {
					rows = 0 // 目标 RowsAffected=0 → fail-closed 返回错误
				}
				mock.ExpectExec(sql).WithArgs(tc.expectArgs[i]...).WillReturnResult(pgxmock.NewResult("UPDATE", rows))
			}
			if tc.expectErr {
				mock.ExpectRollback()
			} else {
				mock.ExpectCommit()
			}

			err := repo.SetDefaultEmbedding(context.Background(), "tenant-a", "model-1", tc.enabled)
			if tc.expectErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.expectErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.expectErr && !errors.Is(err, domain.ErrModelNotFound) {
				t.Fatalf("err = %v, want ErrModelNotFound", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestModelRepo_UpsertDiscovered_newAndExisting(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	now := time.Now()
	discovered := []domain.Model{
		{Name: "new-model", DisplayName: "New", Capabilities: []domain.ModelCapability{domain.CapChat},
			ContextWindow: 8192, MaxTokens: 4096, InputPrice: 10.0, OutputPrice: 30.0, Recommended: true},
		{Name: "existing-model", DisplayName: "Existing", Capabilities: []domain.ModelCapability{domain.CapChat},
			ContextWindow: 128000, MaxTokens: 8192},
	}
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	// disable phase
	mock.ExpectExec(`UPDATE models SET enabled=false, updated_at=now\(\),\s+default_embedding = default_embedding AND 'embedding' = ANY\(capabilities\)\s+WHERE tenant_id=\$1 AND provider_id=\$2 AND provider_managed=true`).
		WithArgs("t1", "p1").WillReturnResult(pgxmock.NewResult("UPDATE", 2))
	// new model -> no existing row -> insert
	mock.ExpectQuery("SELECT id FROM models WHERE tenant_id=\\$1 AND provider_id=\\$2 AND name=\\$3").
		WithArgs("t1", "p1", "new-model").WillReturnError(pgx.ErrNoRows)
	mock.ExpectExec("INSERT INTO models").
		WithArgs(pgxmock.AnyArg(), "t1", "p1", "new-model", "New", []string{"chat"},
			8192, 4096, 10.0, 30.0, true).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	// existing model -> re-enable and sync context metadata
	mock.ExpectQuery("SELECT id FROM models WHERE tenant_id=\\$1 AND provider_id=\\$2 AND name=\\$3").
		WithArgs("t1", "p1", "existing-model").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("m-exist"))
	mock.ExpectExec("UPDATE models SET enabled=true").
		WithArgs(128000, 8192, "m-exist").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	// read back
	mock.ExpectQuery("FROM models WHERE tenant_id=\\$1 AND provider_id=\\$2 ORDER BY name").
		WithArgs("t1", "p1").
		WillReturnRows(pgxmock.NewRows(modelColumns).
			AddRow("m-new", "t1", "p1", "new-model", "New", []string{"chat"}, 8192, 4096, 10.0, 30.0, true, false, true, true, now, now).
			AddRow("m-exist", "t1", "p1", "existing-model", "Existing", []string{"chat"}, 128000, 8192, 0.0, 0.0, false, false, true, true, now, now))
	mock.ExpectCommit()

	result, err := repo.UpsertDiscovered(context.Background(), "t1", "p1", discovered)
	require.NoError(t, err)
	require.Len(t, result, 2)
	require.Equal(t, "new-model", result[0].Name)
	require.Equal(t, []domain.ModelCapability{domain.CapChat}, result[0].Capabilities)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_UpsertDiscovered_disableFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec(`UPDATE models SET enabled=false, updated_at=now\(\),\s+default_embedding = default_embedding AND 'embedding' = ANY\(capabilities\)\s+WHERE tenant_id=\$1 AND provider_id=\$2 AND provider_managed=true`).
		WithArgs("t1", "p1").WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.UpsertDiscovered(context.Background(), "t1", "p1", nil)
	require.ErrorContains(t, err, "disable phase")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_UpsertDiscovered_insertFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec(`UPDATE models SET enabled=false, updated_at=now\(\),\s+default_embedding = default_embedding AND 'embedding' = ANY\(capabilities\)\s+WHERE tenant_id=\$1 AND provider_id=\$2 AND provider_managed=true`).
		WithArgs("t1", "p1").WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectQuery("SELECT id FROM models WHERE tenant_id=\\$1 AND provider_id=\\$2 AND name=\\$3").
		WithArgs("t1", "p1", "new-model").WillReturnError(pgx.ErrNoRows)
	mock.ExpectExec("INSERT INTO models").
		WithArgs(pgxmock.AnyArg(), "t1", "p1", "new-model", "New", []string{"chat"},
			8192, 4096, 10.0, 30.0, true).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.UpsertDiscovered(context.Background(), "t1", "p1",
		[]domain.Model{{Name: "new-model", DisplayName: "New", Capabilities: []domain.ModelCapability{domain.CapChat},
			ContextWindow: 8192, MaxTokens: 4096, InputPrice: 10.0, OutputPrice: 30.0, Recommended: true}})
	require.ErrorContains(t, err, "insert new-model")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_UpsertDiscovered_updateFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec(`UPDATE models SET enabled=false, updated_at=now\(\),\s+default_embedding = default_embedding AND 'embedding' = ANY\(capabilities\)\s+WHERE tenant_id=\$1 AND provider_id=\$2 AND provider_managed=true`).
		WithArgs("t1", "p1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectQuery("SELECT id FROM models WHERE tenant_id=\\$1 AND provider_id=\\$2 AND name=\\$3").
		WithArgs("t1", "p1", "existing").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("m-exist"))
	mock.ExpectExec("UPDATE models SET enabled=true").
		WithArgs(1, 2, "m-exist").WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.UpsertDiscovered(context.Background(), "t1", "p1",
		[]domain.Model{{Name: "existing", ContextWindow: 1, MaxTokens: 2}})
	require.ErrorContains(t, err, "update existing")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelCapsToStrings(t *testing.T) {
	caps := modelCapsToStrings([]domain.ModelCapability{domain.CapChat, domain.CapVision})
	require.Equal(t, []string{"chat", "vision"}, caps)
	require.Empty(t, modelCapsToStrings(nil))
}

func TestStringsToModelCaps(t *testing.T) {
	caps := stringsToModelCaps([]string{"chat", "embedding"})
	require.Equal(t, []domain.ModelCapability{domain.CapChat, domain.CapEmbedding}, caps)
	require.Empty(t, stringsToModelCaps(nil))
}
