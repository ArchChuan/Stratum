// Package persistence holds IAM persistence adapters (token store, sessions).
package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	iamdomain "github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const blacklistKeyPrefix = "rt:blacklist:"

// TokenStore persists refresh tokens in PostgreSQL and caches revocations in Redis.
// The DB stores SHA256(rawToken) — the raw token is only ever in the HTTP cookie.
type TokenStore struct {
	db  tokenStoreDB
	rdb *redis.Client
}

type tokenStoreDB interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// NewTokenStore creates a TokenStore.
func NewTokenStore(db *pgxpool.Pool, rdb *redis.Client) *TokenStore {
	return &TokenStore{db: db, rdb: rdb}
}

func hashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// Create inserts a new refresh token record.
func (s *TokenStore) Create(ctx context.Context, userID, tenantID, rawToken string, ttl time.Duration) error {
	hash := hashToken(rawToken)
	expiresAt := time.Now().UTC().Add(ttl)
	var tid interface{}
	if tenantID != "" {
		tid = tenantID
	}
	_, err := s.db.Exec(ctx,
		`INSERT INTO refresh_tokens (token_hash, user_id, tenant_id, expires_at)
		 VALUES ($1, $2, $3, $4)`,
		hash, userID, tid, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("token_store: create: %w", err)
	}
	return nil
}

// Rotate revokes the old token, adds it to the Redis blacklist, and creates a new one.
func (s *TokenStore) Rotate(ctx context.Context, oldRaw, newRaw string, ttl time.Duration) error {
	oldHash := hashToken(oldRaw)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("token_store: rotate begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID string
	var tenantID *string
	var expiresAt time.Time
	err = tx.QueryRow(ctx,
		`UPDATE refresh_tokens SET revoked_at = NOW() WHERE token_hash = $1 AND revoked_at IS NULL
		 RETURNING user_id, tenant_id, expires_at`,
		oldHash,
	).Scan(&userID, &tenantID, &expiresAt)
	if err != nil {
		return fmt.Errorf("token_store: rotate revoke old: %w", err)
	}

	var tid interface{}
	if tenantID != nil {
		tid = *tenantID
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO refresh_tokens (token_hash, user_id, tenant_id, expires_at)
		 VALUES ($1, $2, $3, $4)`,
		hashToken(newRaw), userID, tid, time.Now().UTC().Add(ttl),
	); err != nil {
		return fmt.Errorf("token_store: rotate create replacement: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("token_store: rotate commit: %w", err)
	}

	remaining := time.Until(expiresAt)
	if remaining > 0 && s.rdb != nil {
		_ = s.rdb.Set(ctx, blacklistKeyPrefix+oldHash, "1", remaining).Err()
	}
	return nil
}

// Revoke marks a token as revoked in DB and adds it to the Redis blacklist.
func (s *TokenStore) Revoke(ctx context.Context, rawToken string) error {
	hash := hashToken(rawToken)

	var expiresAt time.Time
	err := s.db.QueryRow(ctx,
		`UPDATE refresh_tokens SET revoked_at = NOW() WHERE token_hash = $1 AND revoked_at IS NULL
		 RETURNING expires_at`,
		hash,
	).Scan(&expiresAt)
	if err != nil {
		return fmt.Errorf("token_store: revoke: %w", err)
	}

	remaining := time.Until(expiresAt)
	if remaining > 0 && s.rdb != nil {
		_ = s.rdb.Set(ctx, blacklistKeyPrefix+hash, "1", remaining).Err()
	}
	return nil
}

// GetActiveClaims returns the user/tenant session for a non-revoked, non-expired token.
func (s *TokenStore) GetActiveClaims(ctx context.Context, rawToken string) (*iamdomain.StoredSession, error) {
	hash := hashToken(rawToken)
	var userID string
	var tenantID *string
	var avatarURL, githubLogin string
	err := s.db.QueryRow(ctx,
		`SELECT rt.user_id, rt.tenant_id, COALESCE(u.avatar_url, ''), u.github_login
		 FROM refresh_tokens rt
		 JOIN users u ON u.id = rt.user_id
		 WHERE rt.token_hash = $1 AND rt.revoked_at IS NULL AND rt.expires_at > NOW()`,
		hash,
	).Scan(&userID, &tenantID, &avatarURL, &githubLogin)
	if err != nil {
		return nil, fmt.Errorf("token_store: get active claims: %w", err)
	}
	tid := ""
	if tenantID != nil {
		tid = *tenantID
	}
	return &iamdomain.StoredSession{UserID: userID, TenantID: tid, AvatarURL: avatarURL, GitHubLogin: githubLogin}, nil
}

// IsBlacklisted checks the Redis blacklist for a given raw token.
func (s *TokenStore) IsBlacklisted(ctx context.Context, rawToken string) (bool, error) {
	hash := hashToken(rawToken)
	val, err := s.rdb.Get(ctx, blacklistKeyPrefix+hash).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("token_store: redis get: %w", err)
	}
	return val == "1", nil
}
