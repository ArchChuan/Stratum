package wiring

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/config"
	pkgobjectstore "github.com/byteBuilderX/stratum/pkg/storage/objectstore"
)

func TestNewFromExistingCompositionBuildsRevisionStoreBeforeMCP(t *testing.T) {
	store := &compositionObjectStore{}
	container := &Container{RevisionObjectStore: store}
	steps := container.newFromExistingInitialSteps()
	// Verify revision-object-store is built before mcp (the invariant;
	// absolute positions shift as new steps are added before platform).
	var revIdx, mcpIdx = -1, -1
	for i, s := range steps {
		if s.name == "revision-object-store" {
			revIdx = i
		}
		if s.name == "mcp" {
			mcpIdx = i
		}
	}
	if revIdx < 0 || mcpIdx < 0 || revIdx >= mcpIdx {
		t.Fatalf("revision-object-store must precede mcp in NewFromExisting initial composition: %+v", steps)
	}
	if err := container.buildRevisionObjectStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if container.RevisionObjectStore != store {
		t.Fatal("existing revision object store was replaced")
	}
}

func TestRevisionObjectStoreDefaultTraceDisabledDoesNotBlockComposition(t *testing.T) {
	container := &Container{Config: &config.Config{}}
	if err := container.buildRevisionObjectStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if container.RevisionObjectStore != nil {
		t.Fatal("default configuration unexpectedly enabled revision object storage")
	}
}

type compositionObjectStore struct{}

func (*compositionObjectStore) Put(context.Context, pkgobjectstore.Payload) (pkgobjectstore.Reference, error) {
	return pkgobjectstore.Reference{}, nil
}
func (*compositionObjectStore) Get(context.Context, pkgobjectstore.Reference) ([]byte, error) {
	return nil, nil
}
func (*compositionObjectStore) Delete(context.Context, pkgobjectstore.Reference) error { return nil }
func (*compositionObjectStore) DeleteByPrefix(context.Context, string, string) error   { return nil }
