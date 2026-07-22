package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/google/uuid"
)

const revisionCleanupTimeout = 5 * time.Second

var ErrCommitUnknown = port.ErrRevisionCommitUnknown

type CreateRevisionInput = port.CreateRevisionInput

type RevisionService struct {
	store      port.RevisionObjectStore
	repository port.RevisionRepository
}

func NewRevisionService(store port.RevisionObjectStore, repository port.RevisionRepository) *RevisionService {
	return &RevisionService{store: store, repository: repository}
}

func (s *RevisionService) Create(
	ctx context.Context,
	tenantID string,
	input CreateRevisionInput,
) (domain.ResourceRevision, bool, error) {
	if err := s.validateCreate(ctx, tenantID, input); err != nil {
		return domain.ResourceRevision{}, false, err
	}

	canonical, err := json.Marshal(input.Payload)
	if err != nil {
		return domain.ResourceRevision{}, false, fmt.Errorf("revision service: marshal payload: %w", err)
	}
	contentHash := sha256.Sum256(canonical)
	revision := domain.ResourceRevision{
		ID:               uuid.Must(uuid.NewV7()).String(),
		ResourceKind:     input.ResourceKind,
		ResourceID:       strings.TrimSpace(input.ResourceID),
		ParentRevisionID: strings.TrimSpace(input.ParentRevisionID),
		Source:           input.Source,
		Status:           domain.RevisionStatusDraft,
		ContentHash:      hex.EncodeToString(contentHash[:]),
		PayloadRef:       "pending",
		PayloadHash:      "pending",
		SafeSummary:      input.SafeSummary,
		CreatedBy:        strings.TrimSpace(input.CreatedBy),
		CreatedAt:        time.Now().UTC(),
	}
	if err := revision.Validate(); err != nil {
		return domain.ResourceRevision{}, false, fmt.Errorf("revision service: validate revision: %w", err)
	}

	ref, err := s.store.Put(ctx, port.RevisionPayload{
		TenantID:  tenantID,
		Namespace: "evaluation-revisions",
		ID:        revision.ID,
		Value:     json.RawMessage(canonical),
	})
	if err != nil {
		return domain.ResourceRevision{}, false, fmt.Errorf("revision service: store payload: %w", err)
	}
	revision.PayloadRef = ref.URI
	revision.PayloadHash = ref.SHA256
	if err := revision.Validate(); err != nil {
		return domain.ResourceRevision{}, false, s.cleanupError(
			ref,
			fmt.Errorf("revision service: validate stored revision: %w", err),
		)
	}

	stored, created, err := s.repository.Create(ctx, tenantID, revision, input.IdempotencyKey)
	if err != nil {
		if errors.Is(err, port.ErrRevisionCommitUnknown) {
			return domain.ResourceRevision{}, false, fmt.Errorf("revision service: create metadata: %w", err)
		}
		return domain.ResourceRevision{}, false, s.cleanupError(
			ref,
			fmt.Errorf("revision service: create metadata: %w", err),
		)
	}
	if !created {
		_ = s.deleteUploaded(ref)
	}
	return stored, created, nil
}

func (s *RevisionService) Get(
	ctx context.Context,
	tenantID string,
	ref domain.ResourceRef,
) (domain.ResourceRevision, []byte, bool, error) {
	if s == nil || s.store == nil || s.repository == nil {
		return domain.ResourceRevision{}, nil, false, fmt.Errorf("revision service: dependencies unavailable")
	}
	if strings.TrimSpace(tenantID) == "" {
		return domain.ResourceRevision{}, nil, false, fmt.Errorf("revision service: tenant id required")
	}
	if err := ref.Validate(); err != nil {
		return domain.ResourceRevision{}, nil, false, fmt.Errorf("revision service: validate reference: %w", err)
	}
	revision, found, err := s.repository.Get(ctx, tenantID, ref)
	if err != nil || !found {
		return revision, nil, found, err
	}
	payload, err := s.store.Get(ctx, port.RevisionPayloadRef{URI: revision.PayloadRef, SHA256: revision.PayloadHash})
	if err != nil {
		return domain.ResourceRevision{}, nil, false, fmt.Errorf("revision service: load payload: %w", err)
	}
	return revision, payload, true, nil
}

func (s *RevisionService) validateCreate(ctx context.Context, tenantID string, input CreateRevisionInput) error {
	if s == nil || s.store == nil || s.repository == nil {
		return fmt.Errorf("revision service: dependencies unavailable")
	}
	if strings.TrimSpace(tenantID) == "" {
		return fmt.Errorf("revision service: tenant id required")
	}
	if strings.TrimSpace(input.ResourceID) == "" {
		return fmt.Errorf("revision service: resource id required")
	}
	if strings.TrimSpace(input.CreatedBy) == "" {
		return fmt.Errorf("revision service: created by required")
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return fmt.Errorf("revision service: idempotency key required")
	}
	if input.Payload == nil || isNilPayload(input.Payload) {
		return fmt.Errorf("revision service: payload required")
	}
	probe := domain.ResourceRevision{
		ID: "validation", ResourceKind: input.ResourceKind, ResourceID: input.ResourceID,
		Source: input.Source, Status: domain.RevisionStatusDraft, ContentHash: "validation",
		PayloadRef: "validation", PayloadHash: "validation", SafeSummary: input.SafeSummary,
	}
	if err := probe.Validate(); err != nil {
		return fmt.Errorf("revision service: validate input: %w", err)
	}
	if strings.TrimSpace(input.ParentRevisionID) == "" {
		return nil
	}
	parentRef := domain.ResourceRef{
		Kind: input.ResourceKind, ResourceID: input.ResourceID, RevisionID: input.ParentRevisionID,
	}
	parent, found, err := s.repository.Get(ctx, tenantID, parentRef)
	if err != nil {
		return fmt.Errorf("revision service: get parent revision: %w", err)
	}
	if !found || parent.ResourceKind != input.ResourceKind || parent.ResourceID != input.ResourceID {
		return fmt.Errorf("revision service: parent revision does not belong to resource")
	}
	return nil
}

func (s *RevisionService) cleanupError(ref port.RevisionPayloadRef, cause error) error {
	if cleanupErr := s.deleteUploaded(ref); cleanupErr != nil {
		return errors.Join(cause, fmt.Errorf("revision service: cleanup payload: %w", cleanupErr))
	}
	return cause
}

func (s *RevisionService) deleteUploaded(ref port.RevisionPayloadRef) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), revisionCleanupTimeout)
	defer cancel()
	return s.store.Delete(cleanupCtx, ref)
}

func isNilPayload(payload any) bool {
	v := reflect.ValueOf(payload)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
