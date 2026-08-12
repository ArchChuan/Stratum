package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"

	"github.com/byteBuilderX/stratum/internal/mechanism/domain"
)

// errConnReset 包级共享（errors.Is 按指针比较，每次 errors.New 新实例不匹配）。
var errConnReset = errors.New("conn reset")

func profileRepoProfile() domain.Profile {
	return domain.Profile{
		ID:          "0197f8c0-0000-7000-8000-000000000001",
		FamilyKey:   "qwen",
		DisplayName: "通义千问",
		Matcher:     domain.ModelMatcher{FamilyPrefixes: []string{"qwen"}},
		Baseline:    domain.Baseline{Prompts: domain.BaselinePrompts{Compaction: "压缩指令"}},
		Fingerprint: "fp-1",
		Version:     3,
		Status:      domain.ProfileStatusActive,
		CreatedBy:   "u-1",
		CreatedAt:   time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestProfileRepo_Upsert_incrementsVersionOnConflict(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("mock pool: %v", err)
	}
	repo := NewProfileRepo(mock)
	p := profileRepoProfile()

	// 族键冲突时 version 在 SQL 层自增，不依赖调用方传入的 Version。
	mock.ExpectExec(`INSERT INTO model_profiles .*version=model_profiles\.version\+1,.*`).
		WithArgs(pgxmock.AnyArg(), p.FamilyKey, p.DisplayName, pgxmock.AnyArg(), pgxmock.AnyArg(),
			p.Fingerprint, p.Version, p.Status, p.CreatedBy).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := repo.Upsert(context.Background(), p); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestProfileRepo_Upsert_insertsNewProfileWithInitialVersion(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("mock pool: %v", err)
	}
	repo := NewProfileRepo(mock)
	p := profileRepoProfile()
	p.Version = 1 // 首版由 service 保证从 1 开始（SQL 冲突分支自增覆盖本值）

	mock.ExpectExec(`INSERT INTO model_profiles .*ON CONFLICT \(family_key\) DO UPDATE.*`).
		WithArgs(pgxmock.AnyArg(), p.FamilyKey, p.DisplayName, pgxmock.AnyArg(), pgxmock.AnyArg(),
			p.Fingerprint, 1, p.Status, p.CreatedBy).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := repo.Upsert(context.Background(), p); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestProfileRepo_Upsert_propagatesExecError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("mock pool: %v", err)
	}
	repo := NewProfileRepo(mock)
	mock.ExpectExec(`INSERT INTO model_profiles`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(errConnReset)

	err = repo.Upsert(context.Background(), profileRepoProfile())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, errConnReset) {
		t.Fatalf("expected wrapped exec error, got %v", err)
	}
}

func TestProfileRepo_GetByFamilyKey_returnsProfile(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("mock pool: %v", err)
	}
	repo := NewProfileRepo(mock)
	p := profileRepoProfile()

	mock.ExpectQuery(`SELECT id, family_key, display_name, model_matcher, baseline, fingerprint, version, status, created_by, created_at, updated_at FROM model_profiles WHERE family_key=\$1`).
		WithArgs("qwen").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "family_key", "display_name", "model_matcher", "baseline",
			"fingerprint", "version", "status", "created_by", "created_at", "updated_at",
		}).AddRow(p.ID, p.FamilyKey, p.DisplayName,
			[]byte(`{"family_prefixes":["qwen"]}`),
			[]byte(`{"prompts":{"compaction":"压缩指令"}}`),
			p.Fingerprint, p.Version, p.Status, p.CreatedBy, p.CreatedAt, p.UpdatedAt))

	got, ok, err := repo.GetByFamilyKey(context.Background(), "qwen")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got.FamilyKey != "qwen" || got.Version != 3 || got.Baseline.Prompts.Compaction != "压缩指令" {
		t.Fatalf("unexpected profile: %+v", got)
	}
}

func TestProfileRepo_GetByFamilyKey_notFoundReturnsOkFalse(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("mock pool: %v", err)
	}
	repo := NewProfileRepo(mock)
	mock.ExpectQuery(`SELECT id, family_key, display_name, model_matcher, baseline, fingerprint, version, status, created_by, created_at, updated_at FROM model_profiles WHERE family_key=\$1`).
		WithArgs("nope").
		WillReturnError(pgx.ErrNoRows)

	_, ok, err := repo.GetByFamilyKey(context.Background(), "nope")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for missing row")
	}
}

func TestProfileRepo_List_propagatesQueryError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("mock pool: %v", err)
	}
	repo := NewProfileRepo(mock)
	mock.ExpectQuery(`SELECT id, family_key, display_name, model_matcher, baseline, fingerprint, version, status, created_by, created_at, updated_at FROM model_profiles ORDER BY created_at`).
		WillReturnError(errors.New("conn reset"))

	if _, err := repo.List(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestProfileRepo_List_propagatesScanError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("mock pool: %v", err)
	}
	repo := NewProfileRepo(mock)
	rows := pgxmock.NewRows([]string{
		"id", "family_key", "display_name", "model_matcher", "baseline",
		"fingerprint", "version", "status", "created_by", "created_at", "updated_at",
	}).AddRow("bad-id", "qwen", "通义千问",
		`{"family_prefixes":["qwen"]}`, `{"prompts":{}}`,
		"fp", 1, "active", "u-1", time.Now(), time.Now())
	rows.RowError(0, errors.New("scan boom"))
	mock.ExpectQuery(`SELECT id, family_key, display_name, model_matcher, baseline, fingerprint, version, status, created_by, created_at, updated_at FROM model_profiles ORDER BY created_at`).
		WillReturnRows(rows)

	if _, err := repo.List(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}
