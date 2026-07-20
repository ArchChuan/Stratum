package persistence

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type oauthExchangeDB interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type OAuthExchangeStore struct {
	db  oauthExchangeDB
	key [32]byte
}

type oauthExchangePayload struct {
	Kind        iamport.OAuthExchangeKind `json:"kind"`
	Credential  string                    `json:"credential"`
	GitHubLogin string                    `json:"github_login,omitempty"`
	AvatarURL   string                    `json:"avatar_url,omitempty"`
}

func NewOAuthExchangeStore(db oauthExchangeDB, key [32]byte) *OAuthExchangeStore {
	return &OAuthExchangeStore{db: db, key: key}
}

func (s *OAuthExchangeStore) Create(
	ctx context.Context,
	exchange *iamport.OAuthExchange,
	ttl time.Duration,
) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("oauth exchange: generate code: %w", err)
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	credential := exchange.AccessToken
	if exchange.Kind == iamport.OAuthExchangeOnboarding {
		credential = exchange.OnboardingToken
	}
	payload, err := json.Marshal(oauthExchangePayload{
		Kind: exchange.Kind, Credential: credential,
		GitHubLogin: exchange.GitHubLogin, AvatarURL: exchange.AvatarURL,
	})
	if err != nil {
		return "", fmt.Errorf("oauth exchange: encode payload: %w", err)
	}
	ciphertext, err := pkgcrypto.Encrypt(s.key, string(payload))
	if err != nil {
		return "", fmt.Errorf("oauth exchange: encrypt payload: %w", err)
	}
	_, err = s.db.Exec(ctx,
		`INSERT INTO oauth_exchange_codes (code_hash, payload_ciphertext, expires_at)
		 VALUES ($1, $2, $3)`,
		hashOAuthExchangeCode(code), ciphertext, time.Now().UTC().Add(ttl),
	)
	if err != nil {
		return "", fmt.Errorf("oauth exchange: create: %w", err)
	}
	return code, nil
}

func (s *OAuthExchangeStore) Consume(ctx context.Context, code string) (*iamport.OAuthExchange, error) {
	var ciphertext string
	err := s.db.QueryRow(ctx,
		`DELETE FROM oauth_exchange_codes
		 WHERE code_hash = $1 AND expires_at > NOW()
		 RETURNING payload_ciphertext`,
		hashOAuthExchangeCode(code),
	).Scan(&ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, iamport.ErrOAuthExchangeInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("oauth exchange: consume: %w", err)
	}
	payload, err := pkgcrypto.Decrypt(s.key, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("oauth exchange: decrypt payload: %w", err)
	}
	var stored oauthExchangePayload
	if err := json.Unmarshal([]byte(payload), &stored); err != nil {
		return nil, fmt.Errorf("oauth exchange: decode payload: %w", err)
	}
	exchange := iamport.OAuthExchange{
		Kind: stored.Kind, GitHubLogin: stored.GitHubLogin, AvatarURL: stored.AvatarURL,
	}
	if stored.Kind == iamport.OAuthExchangeOnboarding {
		exchange.OnboardingToken = stored.Credential
	} else {
		exchange.AccessToken = stored.Credential
	}
	return &exchange, nil
}

func hashOAuthExchangeCode(code string) string {
	hash := sha256.Sum256([]byte(code))
	return hex.EncodeToString(hash[:])
}
