package milvus

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestMilvusSmoke_Roundtrip is an opt-in connectivity check against a real
// Milvus. It is skipped unless TEST_MILVUS_ADDR ("host:port") is set, so CI and
// offline `go test ./...` runs stay green. When enabled it exercises the exact
// path memory recall depends on: connect → create dim'd collection → insert →
// SearchWithFilter → cleanup.
//
// Run locally with:
//
//	TEST_MILVUS_ADDR=localhost:19530 go test ./pkg/storage/milvus/ -run Smoke -v
func TestMilvusSmoke_Roundtrip(t *testing.T) {
	addr := os.Getenv("TEST_MILVUS_ADDR")
	if addr == "" {
		t.Skip("TEST_MILVUS_ADDR not set; skipping Milvus connectivity smoke test")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("TEST_MILVUS_ADDR %q must be host:port: %v", addr, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	vs := NewVectorStore(host, port, zap.NewNop())
	if err := vs.Connect(ctx); err != nil {
		t.Fatalf("connect to Milvus at %s failed: %v", addr, err)
	}
	defer vs.Close()

	const dim = 8
	coll := "smoke_" + time.Now().Format("150405")
	if err := vs.CreateCollectionWithDim(ctx, coll, dim); err != nil {
		t.Fatalf("create collection: %v", err)
	}
	defer func() {
		if err := vs.DeleteCollection(ctx, coll); err != nil {
			t.Logf("cleanup: delete collection %s failed: %v", coll, err)
		}
	}()

	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = 0.1
	}
	doc := DocumentChunk{
		ID:      "smoke-1",
		UserID:  "u1",
		Scope:   "user",
		Content: "smoke test content",
		Vector:  vec,
	}
	if err := vs.Insert(ctx, coll, []DocumentChunk{doc}, ""); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Milvus insert is async-visible; retry the search briefly before failing.
	var results []SearchResult
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		results, err = vs.SearchWithFilter(ctx, coll, vec, 5, `user_id == "u1" && scope == "user"`)
		if err == nil && len(results) > 0 {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one search result after insert")
	}
	if !strings.Contains(results[0].Content, "smoke test content") {
		t.Fatalf("unexpected result content: %q", results[0].Content)
	}
}
