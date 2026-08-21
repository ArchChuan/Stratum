package infrastructure

import (
	"context"
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
		ID: "m1", ProviderID: "p1", Name: "gpt-4", DisplayName: "GPT-4",
		Capabilities:  []domain.ModelCapability{domain.CapChat, domain.CapVision},
		ContextWindow: 8192, MaxTokens: 4096, InputPrice: 10.0, OutputPrice: 30.0,
		Recommended: true, Enabled: true,
		ContextWindowSource: domain.CapabilitySourceLegacyUnknown,
		MaxTokensSource:     domain.CapabilitySourceLegacyUnknown,
	}
}

var modelColumns = []string{"id", "provider_id", "name", "display_name", "capabilities",
	"context_window", "max_tokens", "input_price", "output_price", "recommended", "default_embedding",
	"enabled", "provider_managed", "sampling_params", "max_temperature",
	"operator_context_window", "operator_max_tokens", "default_output_tokens",
	"fallback_candidates",
	"context_window_source", "max_tokens_source", "context_window_observed_at", "max_tokens_observed_at",
	"created_at", "updated_at"}

func modelRow(m *domain.Model) []any {
	now := time.Now()
	return []any{m.ID, m.ProviderID, m.Name, m.DisplayName,
		[]string{"chat", "vision"}, m.ContextWindow, m.MaxTokens, m.InputPrice, m.OutputPrice,
		m.Recommended, m.DefaultEmbedding, m.Enabled, m.ProviderManaged,
		[]byte("{}"), nil, nil, nil, nil, m.FallbackCandidates,
		m.ContextWindowSource, m.MaxTokensSource, nil, nil, now, now}
}

func TestModelRepo_Create_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	m := modelFixture()
	now := time.Now()
	mock.ExpectQuery("INSERT INTO public.models").
		WithArgs(m.ID, m.ProviderID, m.Name, m.DisplayName, []string{"chat", "vision"},
			m.ContextWindow, m.MaxTokens, m.InputPrice, m.OutputPrice, m.Recommended, m.Enabled,
			m.ProviderManaged, "{}", (*float64)(nil)).
		WillReturnRows(pgxmock.NewRows([]string{"created_at", "updated_at"}).AddRow(now, now))

	require.NoError(t, repo.Create(context.Background(), m))
	require.Equal(t, now, m.CreatedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Create_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	m := modelFixture()
	mock.ExpectQuery("INSERT INTO public.models").
		WithArgs(anyArgs(14)...).WillReturnError(pgx.ErrTxClosed)

	err := repo.Create(context.Background(), m)
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Get_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	m := modelFixture()
	mock.ExpectQuery("FROM public.models WHERE id=\\$1").
		WithArgs("m1").
		WillReturnRows(pgxmock.NewRows(modelColumns).AddRow(modelRow(m)...))

	got, err := repo.Get(context.Background(), "m1")
	require.NoError(t, err)
	require.Equal(t, "gpt-4", got.Name)
	require.Equal(t, []domain.ModelCapability{domain.CapChat, domain.CapVision}, got.Capabilities)
	require.Equal(t, m.DefaultEmbedding, got.DefaultEmbedding)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Get_notFound(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectQuery("FROM public.models WHERE id=\\$1").
		WithArgs("nope").WillReturnError(pgx.ErrNoRows)

	_, err := repo.Get(context.Background(), "nope")
	require.ErrorContains(t, err, "get model")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_List_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectQuery("FROM public.models ORDER BY name").
		WillReturnRows(pgxmock.NewRows(modelColumns).
			AddRow(modelRow(modelFixture())...))

	models, err := repo.List(context.Background(), port.ModelFilter{})
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "gpt-4", models[0].Name)
	require.Equal(t, modelFixture().DefaultEmbedding, models[0].DefaultEmbedding)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_List_empty(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectQuery("FROM public.models ORDER BY name").
		WillReturnRows(pgxmock.NewRows(modelColumns))

	models, err := repo.List(context.Background(), port.ModelFilter{})
	require.NoError(t, err)
	require.Empty(t, models) // nil -> []domain.Model{}, never nil
	require.NotNil(t, models)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_List_withFilters(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	enabled := true
	mock.ExpectQuery("provider_id=\\$1 AND enabled=\\$2 AND \\$3 = ANY\\(capabilities\\)").
		WithArgs("p1", true, "vision").
		WillReturnRows(pgxmock.NewRows(modelColumns).
			AddRow(modelRow(modelFixture())...))

	models, err := repo.List(context.Background(), port.ModelFilter{ProviderID: "p1", Enabled: &enabled, Capability: domain.CapVision})
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_List_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectQuery("FROM public.models ORDER BY name").
		WillReturnError(pgx.ErrTxClosed)

	_, err := repo.List(context.Background(), port.ModelFilter{})
	require.ErrorContains(t, err, "list models")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_List_scanFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	bad := modelFixture()
	mock.ExpectQuery("FROM public.models ORDER BY name").
		WillReturnRows(pgxmock.NewRows(modelColumns).
			AddRow(42, bad.ProviderID, bad.Name, bad.DisplayName, []string{"chat"},
				bad.ContextWindow, bad.MaxTokens, bad.InputPrice, bad.OutputPrice,
				bad.Recommended, bad.DefaultEmbedding, bad.Enabled, bad.ProviderManaged,
				[]byte("{}"), nil, nil, nil, nil, bad.FallbackCandidates,
				bad.ContextWindowSource, bad.MaxTokensSource,
				nil, nil, time.Now(), time.Now()))

	_, err := repo.List(context.Background(), port.ModelFilter{})
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Update_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	m := modelFixture()
	mock.ExpectBegin()
	mock.ExpectExec(`SET LOCAL search_path`).WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec(`UPDATE public.models SET display_name=\$1, capabilities=\$2, context_window=\$3, max_tokens=\$4,\s+input_price=\$5, output_price=\$6, recommended=\$7, enabled=\$8, updated_at=now\(\),\s+sampling_params=\$9, max_temperature=\$10, fallback_candidates=\$11,\s+default_embedding = default_embedding AND \$8 AND 'embedding' = ANY\(\$2\)\s+WHERE id=\$12`).
		WithArgs(m.DisplayName, []string{"chat", "vision"}, m.ContextWindow, m.MaxTokens,
			m.InputPrice, m.OutputPrice, m.Recommended, m.Enabled, "{}", (*float64)(nil), m.FallbackCandidates, m.ID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Update(context.Background(), m, "t1", nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Update_notFound(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec(`SET LOCAL search_path`).WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec(`UPDATE public.models SET display_name=\$1, capabilities=\$2, context_window=\$3, max_tokens=\$4,\s+input_price=\$5, output_price=\$6, recommended=\$7, enabled=\$8, updated_at=now\(\),\s+sampling_params=\$9, max_temperature=\$10, fallback_candidates=\$11,\s+default_embedding = default_embedding AND \$8 AND 'embedding' = ANY\(\$2\)\s+WHERE id=\$12`).
		WithArgs(anyArgs(12)...).WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	err := repo.Update(context.Background(), modelFixture(), "t1", nil)
	require.ErrorContains(t, err, "model not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Update_execFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec(`SET LOCAL search_path`).WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec(`UPDATE public.models SET display_name=\$1, capabilities=\$2, context_window=\$3, max_tokens=\$4,\s+input_price=\$5, output_price=\$6, recommended=\$7, enabled=\$8, updated_at=now\(\),\s+sampling_params=\$9, max_temperature=\$10, fallback_candidates=\$11,\s+default_embedding = default_embedding AND \$8 AND 'embedding' = ANY\(\$2\)\s+WHERE id=\$12`).
		WithArgs(anyArgs(12)...).WillReturnError(pgx.ErrTxClosed)

	err := repo.Update(context.Background(), modelFixture(), "t1", nil)
	require.ErrorContains(t, err, "update model")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Delete_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectExec("DELETE FROM public.models").
		WithArgs("m1").WillReturnResult(pgxmock.NewResult("DELETE", 1))

	require.NoError(t, repo.Delete(context.Background(), "m1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Delete_notFound(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectExec("DELETE FROM public.models").
		WithArgs("m1").WillReturnResult(pgxmock.NewResult("DELETE", 0))

	err := repo.Delete(context.Background(), "m1")
	require.ErrorContains(t, err, "model not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Toggle_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectExec(`UPDATE public.models SET enabled=\$1, updated_at=now\(\),\s+default_embedding = default_embedding AND \$1 AND 'embedding' = ANY\(capabilities\)\s+WHERE id=\$2`).
		WithArgs(false, "m1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	require.NoError(t, repo.Toggle(context.Background(), "m1", false))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_Toggle_notFound(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectExec(`UPDATE public.models SET enabled=\$1, updated_at=now\(\),\s+default_embedding = default_embedding AND \$1 AND 'embedding' = ANY\(capabilities\)\s+WHERE id=\$2`).
		WithArgs(anyArgs(2)...).WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	err := repo.Toggle(context.Background(), "m1", true)
	require.ErrorContains(t, err, "model not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_UpsertDiscovered_newAndExisting(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	now := time.Now()
	discovered := []domain.Model{
		{Name: "new-model", DisplayName: "New", Capabilities: []domain.ModelCapability{domain.CapChat},
			ContextWindow: 8192, MaxTokens: 4096, InputPrice: 10.0, OutputPrice: 30.0, Recommended: true,
			ContextWindowSource: domain.CapabilitySourceProviderAPI, MaxTokensSource: domain.CapabilitySourceProviderAPI},
		{Name: "existing-model", DisplayName: "Existing", Capabilities: []domain.ModelCapability{domain.CapChat},
			ContextWindow: 128000, MaxTokens: 8192,
			ContextWindowSource: domain.CapabilitySourceProviderAPI, MaxTokensSource: domain.CapabilitySourceProviderAPI},
	}
	mock.ExpectBegin()
	// disable phase
	mock.ExpectExec(`UPDATE public.models SET enabled=false, updated_at=now\(\),\s+default_embedding = default_embedding AND 'embedding' = ANY\(capabilities\)\s+WHERE provider_id=\$1 AND provider_managed=true`).
		WithArgs("p1").WillReturnResult(pgxmock.NewResult("UPDATE", 2))
	// new model -> no existing row -> insert
	mock.ExpectQuery("SELECT id FROM public.models WHERE provider_id=\\$1 AND name=\\$2").
		WithArgs("p1", "new-model").WillReturnError(pgx.ErrNoRows)
	mock.ExpectExec("INSERT INTO public.models").
		WithArgs(pgxmock.AnyArg(), "p1", "new-model", "New", []string{"chat"},
			8192, 4096, 10.0, 30.0, true, domain.CapabilitySourceProviderAPI, domain.CapabilitySourceProviderAPI).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	// existing model -> re-enable and sync context metadata
	mock.ExpectQuery("SELECT id FROM public.models WHERE provider_id=\\$1 AND name=\\$2").
		WithArgs("p1", "existing-model").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("m-exist"))
	mock.ExpectExec("UPDATE public.models SET enabled=true").
		WithArgs(128000, 8192, domain.CapabilitySourceProviderAPI, domain.CapabilitySourceProviderAPI, "m-exist").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	// read back
	mock.ExpectQuery("FROM public.models WHERE provider_id=\\$1 ORDER BY name").
		WithArgs("p1").
		WillReturnRows(pgxmock.NewRows(modelColumns).
			AddRow("m-new", "p1", "new-model", "New", []string{"chat"}, 8192, 4096, 10.0, 30.0, true, false, true, true,
				[]byte("{}"), nil, nil, nil, nil, nil, domain.CapabilitySourceProviderAPI, domain.CapabilitySourceProviderAPI,
				nil, nil, now, now).
			AddRow("m-exist", "p1", "existing-model", "Existing", []string{"chat"}, 128000, 8192, 0.0, 0.0, false, false, true, true,
				[]byte("{}"), nil, nil, nil, nil, nil, domain.CapabilitySourceProviderAPI, domain.CapabilitySourceProviderAPI,
				nil, nil, now, now))
	mock.ExpectCommit()

	result, err := repo.UpsertDiscovered(context.Background(), "p1", discovered)
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
	mock.ExpectExec(`UPDATE public.models SET enabled=false, updated_at=now\(\),\s+default_embedding = default_embedding AND 'embedding' = ANY\(capabilities\)\s+WHERE provider_id=\$1 AND provider_managed=true`).
		WithArgs("p1").WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.UpsertDiscovered(context.Background(), "p1", nil)
	require.ErrorContains(t, err, "disable phase")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_UpsertDiscovered_insertFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE public.models SET enabled=false, updated_at=now\(\),\s+default_embedding = default_embedding AND 'embedding' = ANY\(capabilities\)\s+WHERE provider_id=\$1 AND provider_managed=true`).
		WithArgs("p1").WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectQuery("SELECT id FROM public.models WHERE provider_id=\\$1 AND name=\\$2").
		WithArgs("p1", "new-model").WillReturnError(pgx.ErrNoRows)
	mock.ExpectExec("INSERT INTO public.models").
		WithArgs(pgxmock.AnyArg(), "p1", "new-model", "New", []string{"chat"},
			8192, 4096, 10.0, 30.0, true, pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.UpsertDiscovered(context.Background(), "p1",
		[]domain.Model{{Name: "new-model", DisplayName: "New", Capabilities: []domain.ModelCapability{domain.CapChat},
			ContextWindow: 8192, MaxTokens: 4096, InputPrice: 10.0, OutputPrice: 30.0, Recommended: true}})
	require.ErrorContains(t, err, "insert new-model")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelRepo_UpsertDiscovered_updateFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockModelRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE public.models SET enabled=false, updated_at=now\(\),\s+default_embedding = default_embedding AND 'embedding' = ANY\(capabilities\)\s+WHERE provider_id=\$1 AND provider_managed=true`).
		WithArgs("p1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectQuery("SELECT id FROM public.models WHERE provider_id=\\$1 AND name=\\$2").
		WithArgs("p1", "existing").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("m-exist"))
	mock.ExpectExec("UPDATE public.models SET enabled=true").
		WithArgs(1, 2, pgxmock.AnyArg(), pgxmock.AnyArg(), "m-exist").WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.UpsertDiscovered(context.Background(), "p1",
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
