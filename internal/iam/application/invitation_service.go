package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/internal/iam/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

var (
	ErrInvalidInvitationEmail = errors.New("iam: verified invitation email required")
	ErrInvalidInvitationRole  = errors.New("iam: invitation role must be admin or member")
)

type InvitationService struct {
	repo port.InvitationRepo
}

func NewInvitationService(repo port.InvitationRepo) *InvitationService {
	return &InvitationService{repo: repo}
}

func (s *InvitationService) Create(
	ctx context.Context,
	tenantID, invitedBy, callerRole, email, role string,
) (string, error) {
	if callerRole != "owner" && callerRole != "admin" {
		return "", ErrForbiddenAdminOrOwner
	}
	email = normalizeInvitationEmail(email)
	if email == "" {
		return "", ErrInvalidInvitationEmail
	}
	if role != "admin" && role != "member" {
		return "", ErrInvalidInvitationRole
	}
	raw := make([]byte, constants.InvitationCodeSize)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("invitation: generate code: %w", err)
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	if err := s.repo.Create(ctx, domain.TenantInvitation{
		TenantID: tenantID, Email: email, Role: role, InvitedBy: invitedBy,
		CodeHash: hashInvitationCode(code), ExpiresAt: time.Now().UTC().Add(constants.InviteTokenTTL),
	}); err != nil {
		return "", fmt.Errorf("invitation: create: %w", err)
	}
	return code, nil
}

func (s *InvitationService) Join(
	ctx context.Context,
	code string,
	identity domain.InvitationIdentity,
) (*domain.InvitationJoinResult, error) {
	identity.Email = normalizeInvitationEmail(identity.Email)
	if strings.TrimSpace(code) == "" || identity.Email == "" {
		return nil, ErrInvalidInvitationEmail
	}
	result, err := s.repo.ConsumeAndJoin(ctx, domain.InvitationJoinInput{
		CodeHash: hashInvitationCode(strings.TrimSpace(code)), Identity: identity, Now: time.Now().UTC(),
	})
	if err != nil {
		return nil, fmt.Errorf("invitation: join: %w", err)
	}
	return result, nil
}

func (s *InvitationService) JoinExisting(
	ctx context.Context,
	code, userID string,
) (*domain.InvitationJoinResult, error) {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(userID) == "" {
		return nil, domain.ErrInvitationInvalid
	}
	result, err := s.repo.ConsumeAndJoinExisting(ctx, domain.ExistingInvitationJoinInput{
		CodeHash: hashInvitationCode(strings.TrimSpace(code)), UserID: userID, Now: time.Now().UTC(),
	})
	if err != nil {
		return nil, fmt.Errorf("invitation: join existing user: %w", err)
	}
	return result, nil
}

func normalizeInvitationEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func hashInvitationCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}
