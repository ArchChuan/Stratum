// Package auth provides JWT token management and authentication.
package auth

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CreateTenantInput holds the fields needed to create a new tenant.
type CreateTenantInput struct {
	UserID    string
	Name      string
	GitHubOrg string
}

// CreateTenantResult is returned on successful tenant creation.
type CreateTenantResult struct {
	TenantID   string
	SchemaName string
}

// JoinTenantInput holds the fields needed to join an existing tenant via invitation.
type JoinTenantInput struct {
	UserID          string
	InvitationToken string
}

// OnboardService handles tenant creation and joining logic.
type OnboardService struct {
	db *pgxpool.Pool
}

// NewOnboardService creates an OnboardService.
func NewOnboardService(db *pgxpool.Pool) *OnboardService {
	return &OnboardService{db: db}
}

// CreateTenant runs a transaction that:
//  1. Inserts a new row in `tenants`
//  2. Inserts the creator as `admin` in `tenant_members`
//  3. Executes `CREATE SCHEMA tenant_{id}`
func (s *OnboardService) CreateTenant(ctx context.Context, in CreateTenantInput) (*CreateTenantResult, error) {
	tenantID := uuid.New().String()
	schemaName := "tenant_" + tenantID

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("onboard: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	slug := in.GitHubOrg
	if slug == "" {
		slug = tenantID[:8]
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO tenants (id, name, slug, github_org_name) VALUES ($1, $2, $3, $4)`,
		tenantID, in.Name, slug, in.GitHubOrg,
	)
	if err != nil {
		return nil, fmt.Errorf("onboard: insert tenant: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO tenant_members (tenant_id, user_id, role) VALUES ($1, $2, 'admin')`,
		tenantID, in.UserID,
	)
	if err != nil {
		return nil, fmt.Errorf("onboard: insert tenant_member: %w", err)
	}

	// schema name is safe: uses UUID chars (hex + hyphens) only
	_, err = tx.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS "%s"`, schemaName))
	if err != nil {
		return nil, fmt.Errorf("onboard: create schema: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("onboard: commit: %w", err)
	}

	return &CreateTenantResult{TenantID: tenantID, SchemaName: schemaName}, nil
}

// JoinTenant validates an invitation token and inserts the user into the tenant.
func (s *OnboardService) JoinTenant(ctx context.Context, in JoinTenantInput) error {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("onboard: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var tenantID, role string
	err = tx.QueryRow(ctx,
		`UPDATE invitations SET accepted_at = NOW()
		 WHERE token_hash = $1 AND accepted_at IS NULL AND expires_at > NOW()
		 RETURNING tenant_id, role`,
		in.InvitationToken,
	).Scan(&tenantID, &role)
	if err != nil {
		return fmt.Errorf("onboard: invalid or expired invitation token: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO tenant_members (tenant_id, user_id, role)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (tenant_id, user_id) DO NOTHING`,
		tenantID, in.UserID, role,
	)
	if err != nil {
		return fmt.Errorf("onboard: insert tenant_member: %w", err)
	}

	return tx.Commit(ctx)
}
