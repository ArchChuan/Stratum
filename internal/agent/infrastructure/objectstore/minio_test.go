package objectstore

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/minio/minio-go/v7"
)

type fakeObjectPutter struct {
	bucket string
	key    string
	body   []byte
}

func (f *fakeObjectPutter) BucketExists(context.Context, string) (bool, error) {
	return true, nil
}

func (f *fakeObjectPutter) MakeBucket(context.Context, string, minio.MakeBucketOptions) error {
	return nil
}

func (f *fakeObjectPutter) PutObject(
	_ context.Context, bucket, key string, reader io.Reader, _ int64, _ minio.PutObjectOptions,
) (minio.UploadInfo, error) {
	f.bucket, f.key = bucket, key
	f.body, _ = io.ReadAll(reader)
	return minio.UploadInfo{}, nil
}

func TestStorePutRedactsEncryptsAndReturnsOpaqueReference(t *testing.T) {
	client := &fakeObjectPutter{}
	key := pkgcrypto.DeriveAESKey("payload-test-key")
	store := NewStore(client, "trace-evidence", key)

	ref, err := store.Put(context.Background(), port.TracePayload{
		TenantID: "tenant-1", TraceID: "trace-1", Kind: "tool-result",
		Value: map[string]any{"answer": "42", "api_key": "do-not-store"},
	})
	if err != nil {
		t.Fatalf("Put() error: %v", err)
	}
	if client.bucket != "trace-evidence" || !strings.HasPrefix(client.key, "tenant-1/trace-1/") {
		t.Fatalf("bucket=%q key=%q", client.bucket, client.key)
	}
	if bytes.Contains(client.body, []byte("42")) || bytes.Contains(client.body, []byte("do-not-store")) {
		t.Fatalf("object contains plaintext: %q", client.body)
	}
	plain, err := pkgcrypto.Decrypt(key, string(client.body))
	if err != nil {
		t.Fatalf("Decrypt() error: %v", err)
	}
	if !strings.Contains(plain, `[REDACTED]`) || strings.Contains(plain, "do-not-store") {
		t.Fatalf("decrypted payload not redacted: %s", plain)
	}
	if ref.Reference == "" || ref.SHA256 == "" || ref.SizeBytes <= 0 {
		t.Fatalf("reference = %#v", ref)
	}
}
