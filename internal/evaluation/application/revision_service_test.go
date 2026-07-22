package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
)

func TestRevisionServiceCreatesEncryptedRevisionMetadata(t *testing.T) {
	store := &fakeRevisionObjectStore{ref: port.RevisionPayloadRef{URI: "object://revisions/payload.enc", SHA256: "payload-hash"}}
	repo := &fakeRevisionRepository{}
	service := NewRevisionService(store, repo)

	revision, created, err := service.Create(context.Background(), "tenant-1", validCreateRevisionInput())
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if !created || revision.ID == "" {
		t.Fatalf("unexpected creation result: created=%v revision=%+v", created, revision)
	}
	if store.putCalls != 1 || repo.createCalls != 1 {
		t.Fatalf("unexpected calls: put=%d create=%d", store.putCalls, repo.createCalls)
	}
	canonical := []byte(`{"instructions":"classify","temperature":0.2}`)
	hash := sha256.Sum256(canonical)
	if revision.ContentHash != hex.EncodeToString(hash[:]) || revision.PayloadHash != "payload-hash" {
		t.Fatalf("unexpected hashes: content=%q payload=%q", revision.ContentHash, revision.PayloadHash)
	}
	if store.payload.Namespace != "evaluation-revisions" || store.payload.TenantID != "tenant-1" {
		t.Fatalf("unexpected object payload: %+v", store.payload)
	}
}

func TestRevisionServiceReturnsObjectStoreFailureWithoutRepositoryWrite(t *testing.T) {
	storeErr := errors.New("object unavailable")
	store := &fakeRevisionObjectStore{putErr: storeErr}
	repo := &fakeRevisionRepository{}
	service := NewRevisionService(store, repo)

	_, _, err := service.Create(context.Background(), "tenant-1", validCreateRevisionInput())
	if !errors.Is(err, storeErr) {
		t.Fatalf("expected object store error, got %v", err)
	}
	if repo.createCalls != 0 || store.deleteCalls != 0 {
		t.Fatalf("unexpected calls: create=%d delete=%d", repo.createCalls, store.deleteCalls)
	}
}

func TestRevisionServiceCleansObjectAfterRepositoryFailure(t *testing.T) {
	repositoryErr := errors.New("insert failed")
	cleanupErr := errors.New("cleanup failed")
	store := &fakeRevisionObjectStore{
		ref:       port.RevisionPayloadRef{URI: "object://revisions/payload.enc", SHA256: "payload-hash"},
		deleteErr: cleanupErr,
	}
	repo := &fakeRevisionRepository{createErr: repositoryErr}
	service := NewRevisionService(store, repo)

	_, _, err := service.Create(context.Background(), "tenant-1", validCreateRevisionInput())
	if !errors.Is(err, repositoryErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("expected joined insert and cleanup errors, got %v", err)
	}
	if store.deleteCalls != 1 || store.deletedRef.URI != store.ref.URI {
		t.Fatalf("uploaded object was not cleaned up: calls=%d ref=%+v", store.deleteCalls, store.deletedRef)
	}
}

func TestRevisionServiceDoesNotDeleteOnUnknownCommitOutcome(t *testing.T) {
	store := &fakeRevisionObjectStore{ref: port.RevisionPayloadRef{URI: "object://revisions/payload.enc", SHA256: "payload-hash"}}
	repo := &fakeRevisionRepository{createErr: ErrCommitUnknown}
	service := NewRevisionService(store, repo)
	_, _, err := service.Create(context.Background(), "tenant-1", validCreateRevisionInput())
	if !errors.Is(err, ErrCommitUnknown) || store.deleteCalls != 0 {
		t.Fatalf("unknown commit must preserve payload: err=%v deletes=%d", err, store.deleteCalls)
	}
}

func TestRevisionServiceDuplicateIdempotencyReturnsExisting(t *testing.T) {
	existing := validRevision()
	existing.ID = "revision-existing"
	store := &fakeRevisionObjectStore{ref: port.RevisionPayloadRef{URI: "object://revisions/new.enc", SHA256: "new-hash"}}
	repo := &fakeRevisionRepository{createResult: existing, created: false}
	service := NewRevisionService(store, repo)

	revision, created, err := service.Create(context.Background(), "tenant-1", validCreateRevisionInput())
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created || revision.ID != existing.ID {
		t.Fatalf("expected existing revision, got created=%v revision=%+v", created, revision)
	}
	if store.deleteCalls != 1 {
		t.Fatalf("duplicate upload was not cleaned up: calls=%d", store.deleteCalls)
	}
}

func TestRevisionServiceDuplicateCleanupFailureStillReturnsExisting(t *testing.T) {
	existing := validRevision()
	store := &fakeRevisionObjectStore{ref: port.RevisionPayloadRef{URI: "object://revisions/new.enc", SHA256: "new-hash"}, deleteErr: errors.New("delete failed")}
	repo := &fakeRevisionRepository{createResult: existing, created: false}
	revision, created, err := NewRevisionService(store, repo).Create(context.Background(), "tenant-1", validCreateRevisionInput())
	if err != nil || created || revision.ID != existing.ID {
		t.Fatalf("duplicate must remain successful despite cleanup error: revision=%+v created=%v err=%v", revision, created, err)
	}
}

func TestRevisionServiceRejectsCrossKindParentBeforeUpload(t *testing.T) {
	parent := validRevision()
	parent.ResourceKind = domain.ResourceKindAgent
	parent.ResourceID = "agent-1"
	store := &fakeRevisionObjectStore{}
	repo := &fakeRevisionRepository{getResult: parent, getFound: true}
	service := NewRevisionService(store, repo)
	input := validCreateRevisionInput()
	input.ParentRevisionID = parent.ID

	_, _, err := service.Create(context.Background(), "tenant-1", input)
	if err == nil || !strings.Contains(err.Error(), "parent revision") {
		t.Fatalf("expected parent revision validation error, got %v", err)
	}
	if store.putCalls != 0 || repo.createCalls != 0 {
		t.Fatalf("validation occurred after persistence: put=%d create=%d", store.putCalls, repo.createCalls)
	}
}

func TestRevisionServiceRejectsSensitiveSafeSummaryBeforeUpload(t *testing.T) {
	store := &fakeRevisionObjectStore{}
	repo := &fakeRevisionRepository{}
	service := NewRevisionService(store, repo)
	input := validCreateRevisionInput()
	input.SafeSummary = map[string]any{"nested": map[string]any{"api_key": "synthetic-value"}}

	_, _, err := service.Create(context.Background(), "tenant-1", input)
	if err == nil || !strings.Contains(err.Error(), "sensitive key") {
		t.Fatalf("expected sensitive summary error, got %v", err)
	}
	if store.putCalls != 0 || repo.createCalls != 0 {
		t.Fatalf("validation occurred after persistence: put=%d create=%d", store.putCalls, repo.createCalls)
	}
}

func validCreateRevisionInput() CreateRevisionInput {
	return CreateRevisionInput{
		ResourceKind:   domain.ResourceKindSkill,
		ResourceID:     "skill-1",
		CreatedBy:      "user-1",
		IdempotencyKey: "request-1",
		Source:         domain.RevisionSourceManual,
		Payload:        map[string]any{"temperature": 0.2, "instructions": "classify"},
		SafeSummary:    map[string]any{"changed_fields": []any{"instructions"}},
	}
}

func validRevision() domain.ResourceRevision {
	return domain.ResourceRevision{
		ID:           "revision-1",
		ResourceKind: domain.ResourceKindSkill,
		ResourceID:   "skill-1",
		Source:       domain.RevisionSourceManual,
		Status:       domain.RevisionStatusDraft,
		ContentHash:  "content-hash",
		PayloadRef:   "object://revisions/payload.enc",
		PayloadHash:  "payload-hash",
		SafeSummary:  map[string]any{},
		CreatedBy:    "user-1",
	}
}

type fakeRevisionObjectStore struct {
	ref         port.RevisionPayloadRef
	putErr      error
	deleteErr   error
	payload     port.RevisionPayload
	deletedRef  port.RevisionPayloadRef
	putCalls    int
	deleteCalls int
}

func (f *fakeRevisionObjectStore) Put(_ context.Context, payload port.RevisionPayload) (port.RevisionPayloadRef, error) {
	f.putCalls++
	f.payload = payload
	return f.ref, f.putErr
}

func (f *fakeRevisionObjectStore) Get(context.Context, port.RevisionPayloadRef) ([]byte, error) {
	return nil, nil
}

func (f *fakeRevisionObjectStore) Delete(_ context.Context, ref port.RevisionPayloadRef) error {
	f.deleteCalls++
	f.deletedRef = ref
	return f.deleteErr
}

type fakeRevisionRepository struct {
	createResult domain.ResourceRevision
	created      bool
	createErr    error
	getResult    domain.ResourceRevision
	getFound     bool
	createCalls  int
}

func (f *fakeRevisionRepository) Create(
	_ context.Context,
	_ string,
	revision domain.ResourceRevision,
	_ string,
) (domain.ResourceRevision, bool, error) {
	f.createCalls++
	if f.createErr != nil {
		return domain.ResourceRevision{}, false, f.createErr
	}
	if f.createResult.ID != "" {
		return f.createResult, f.created, nil
	}
	return revision, true, nil
}

func (f *fakeRevisionRepository) Get(
	_ context.Context,
	_ string,
	_ domain.ResourceRef,
) (domain.ResourceRevision, bool, error) {
	return f.getResult, f.getFound, nil
}
