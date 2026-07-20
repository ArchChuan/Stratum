package persistence

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v2"
)

func TestTokenStoreRotateRollsBackWhenReplacementInsertFails(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := &TokenStore{db: pool}
	expiresAt := time.Now().Add(time.Hour)

	pool.ExpectBegin()
	pool.ExpectQuery(regexp.QuoteMeta(
		`UPDATE refresh_tokens SET revoked_at = NOW() WHERE token_hash = $1 AND revoked_at IS NULL
		 RETURNING user_id, tenant_id, expires_at`,
	)).WithArgs(hashToken("old")).WillReturnRows(pgxmock.NewRows([]string{"user_id", "tenant_id", "expires_at"}).
		AddRow("00000000-0000-0000-0000-000000000001", nil, expiresAt))
	pool.ExpectExec("INSERT INTO refresh_tokens").
		WithArgs(hashToken("new"), "00000000-0000-0000-0000-000000000001", nil, pgxmock.AnyArg()).
		WillReturnError(errors.New("insert failed"))
	pool.ExpectRollback()

	if err := store.Rotate(context.Background(), "old", "new", time.Hour); err == nil {
		t.Fatal("expected replacement insert failure")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
