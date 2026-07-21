//go:build integration

package objectstore

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func TestStoreRealMinIOEncryptedRoundTrip(t *testing.T) {
	endpoint := os.Getenv("TEST_MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("TEST_MINIO_ENDPOINT not set")
	}
	accessKey := envOrDefault("TEST_MINIO_ACCESS_KEY", "trace-test")
	secretKey := envOrDefault("TEST_MINIO_SECRET_KEY", "trace-test-secret")
	client, err := minio.New(endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(accessKey, secretKey, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	bucket := envOrDefault("TEST_MINIO_BUCKET", "stratum-trace-evidence-test")
	if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		exists, existsErr := client.BucketExists(ctx, bucket)
		if existsErr != nil || !exists {
			t.Fatalf("MakeBucket: %v", err)
		}
	}
	t.Cleanup(func() {
		objects := client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true})
		for object := range objects {
			_ = client.RemoveObject(ctx, bucket, object.Key, minio.RemoveObjectOptions{})
		}
		_ = client.RemoveBucket(ctx, bucket)
	})
	key := pkgcrypto.DeriveAESKey("real-minio-test-key")
	store := NewStore(client, bucket, key)
	ref, err := store.Put(ctx, port.TracePayload{
		TenantID: "tenant-1", TraceID: "trace-1", Kind: "tool-result",
		Value: map[string]any{"result": "42", "token": "do-not-store"},
	})
	if err != nil {
		t.Fatal(err)
	}
	objectKey := strings.TrimPrefix(ref.Reference, "object://"+bucket+"/")
	object, err := client.GetObject(ctx, bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := io.ReadAll(object)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := pkgcrypto.Decrypt(key, string(encrypted))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "do-not-store") || !strings.Contains(plain, `[REDACTED]`) {
		t.Fatalf("payload redaction failed: %s", plain)
	}
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
