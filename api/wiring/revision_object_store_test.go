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
	if len(steps) < 3 || steps[0].name != "platform" || steps[1].name != "revision-object-store" ||
		steps[2].name != "mcp" {
		t.Fatalf("unexpected NewFromExisting initial composition: %+v", steps)
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
