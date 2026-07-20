package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
)

type encryptedOAuthPayload struct {
	key       [32]byte
	plaintext string
}

func (m encryptedOAuthPayload) Match(value interface{}) bool {
	ciphertext, ok := value.(string)
	if !ok || strings.Contains(ciphertext, "access-token") {
		return false
	}
	plain, err := pkgcrypto.Decrypt(m.key, ciphertext)
	return err == nil && plain == m.plaintext
}

func TestOAuthExchangeStoreCreateStoresHashAndConsumeDeletesAtomically(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	key := pkgcrypto.DeriveAESKey("oauth-exchange-test-key")
	store := NewOAuthExchangeStore(pool, key)
	payload := &iamport.OAuthExchange{Kind: iamport.OAuthExchangeLogin, AccessToken: "access-token"}
	encoded, err := json.Marshal(oauthExchangePayload{
		Kind: payload.Kind, Credential: payload.AccessToken,
	})
	if err != nil {
		t.Fatal(err)
	}

	pool.ExpectExec(regexp.QuoteMeta(`INSERT INTO oauth_exchange_codes (code_hash, payload_ciphertext, expires_at)
		 VALUES ($1, $2, $3)`)).
		WithArgs(pgxmock.AnyArg(), encryptedOAuthPayload{key: key, plaintext: string(encoded)}, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	code, err := store.Create(context.Background(), payload, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if code == "" || code == "access-token" {
		t.Fatalf("Create returned unsafe code %q", code)
	}

	hash := sha256.Sum256([]byte(code))
	ciphertext, err := pkgcrypto.Encrypt(key, string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	pool.ExpectQuery(regexp.QuoteMeta(`DELETE FROM oauth_exchange_codes
		 WHERE code_hash = $1 AND expires_at > NOW()
		 RETURNING payload_ciphertext`)).
		WithArgs(hex.EncodeToString(hash[:])).
		WillReturnRows(pgxmock.NewRows([]string{"payload_ciphertext"}).AddRow(ciphertext))
	got, err := store.Consume(context.Background(), code)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != payload.Kind || got.AccessToken != payload.AccessToken {
		t.Fatalf("Consume()=%#v, want %#v", got, payload)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthExchangeStoreConsumeRejectsMissingOrExpiredCode(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewOAuthExchangeStore(pool, pkgcrypto.DeriveAESKey("oauth-exchange-test-key"))

	pool.ExpectQuery("DELETE FROM oauth_exchange_codes").
		WithArgs(pgxmock.AnyArg()).
		WillReturnError(pgx.ErrNoRows)
	_, err = store.Consume(context.Background(), "expired")
	if err != iamport.ErrOAuthExchangeInvalid {
		t.Fatalf("Consume error=%v, want ErrOAuthExchangeInvalid", err)
	}
}
