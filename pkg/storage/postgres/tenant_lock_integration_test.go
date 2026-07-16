//go:build integration

package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestWithSchemaProvisionLockSerializesConnections(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	firstPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create first pool: %v", err)
	}
	defer firstPool.Close()
	secondPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create second pool: %v", err)
	}
	defer secondPool.Close()

	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- WithSchemaProvisionLock(ctx, firstPool, func(context.Context) error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	<-firstEntered

	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- WithSchemaProvisionLock(ctx, secondPool, func(context.Context) error {
			close(secondEntered)
			return nil
		})
	}()

	select {
	case <-secondEntered:
		t.Fatal("second connection entered while first held the schema provision lock")
	case <-time.After(200 * time.Millisecond):
	}

	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first lock: %v", err)
	}
	select {
	case <-secondEntered:
	case <-ctx.Done():
		t.Fatalf("second connection did not enter after release: %v", ctx.Err())
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second lock: %v", err)
	}
}
