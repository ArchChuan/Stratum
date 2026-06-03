// Package auth provides JWT token management and authentication.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const blacklistKeyPrefix = "rt:blacklist:"

// TokenStore persists refresh tokens in PostgreSQL and caches revocations in Redis.
// The DB stores SHA256(rawToken) — the raw token is only ever in the HTTP cookie.
type TokenStore struct {
	db  *pgxpool.Pool
	rdb *redis.Client
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

	var userID string
	var tenantID *string
	var expiresAt time.Time
	err := s.db.QueryRow(ctx,
		`UPDATE refresh_tokens SET revoked_at = NOW() WHERE token_hash = $1 AND revoked_at IS NULL
		 RETURNING user_id, tenant_id, expires_at`,
		oldHash,
	).Scan(&userID, &tenantID, &expiresAt)
	if err != nil {
		return fmt.Errorf("token_store: rotate revoke old: %w", err)
	}

	remaining := time.Until(expiresAt)
	if remaining > 0 {
		s.rdb.Set(ctx, blacklistKeyPrefix+oldHash, "1", remaining)
	}

	tid := ""
	if tenantID != nil {
		tid = *tenantID
	}
	return s.Create(ctx, userID, tid, newRaw, ttl)
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
	if remaining > 0 {
		s.rdb.Set(ctx, blacklistKeyPrefix+hash, "1", remaining)
	}
	return nil
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
