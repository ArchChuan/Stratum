package persistence

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
)

func TestPlatformRepositoryGetValue(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	repo := &PlatformRepository{pool: mock}

	t.Run("found", func(t *testing.T) {
		mock.ExpectQuery(`SELECT value FROM public\.platform_settings WHERE key = \$1`).
			WithArgs("evaluation.optimizer.temperature").
			WillReturnRows(pgxmock.NewRows([]string{"value"}).AddRow([]byte(`0.5`)))

		raw, present, err := repo.GetValue(context.Background(), "evaluation.optimizer.temperature")
		if err != nil {
			t.Fatal(err)
		}
		if !present || string(raw) != `0.5` {
			t.Fatalf("got (%q, %v), want (0.5, true)", raw, present)
		}
	})

	t.Run("absent is not an error", func(t *testing.T) {
		mock.ExpectQuery(`SELECT value FROM public\.platform_settings WHERE key = \$1`).
			WithArgs("missing.key").
			WillReturnError(pgx.ErrNoRows)

		raw, present, err := repo.GetValue(context.Background(), "missing.key")
		if err != nil || present || raw != nil {
			t.Fatalf("got (%q, %v, %v), want (nil, false, nil)", raw, present, err)
		}
	})
}

func TestPlatformRepositorySetValueUpsert(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	repo := &PlatformRepository{pool: mock}

	mock.ExpectQuery(`INSERT INTO public\.platform_settings`).
		WithArgs("trace.capture_parameters", `true`, "admin-1").
		WillReturnRows(pgxmock.NewRows([]string{"key"}).AddRow("trace.capture_parameters"))

	if err := repo.SetValue(context.Background(), "trace.capture_parameters", json.RawMessage(`true`), "admin-1"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPlatformRepositoryGetAll(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	repo := &PlatformRepository{pool: mock}

	mock.ExpectQuery(`SELECT key, value, updated_by, updated_at FROM public\.platform_settings`).
		WillReturnRows(pgxmock.NewRows([]string{"key", "value", "updated_by", "updated_at"}).
			AddRow("a.key", []byte(`1`), "admin-1", time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)))

	values, err := repo.GetAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Key != "a.key" || string(values[0].Value) != `1` {
		t.Fatalf("unexpected rows: %+v", values)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
