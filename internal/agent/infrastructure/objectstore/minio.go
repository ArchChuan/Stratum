package objectstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/byteBuilderX/stratum/pkg/observability"
	genericstore "github.com/byteBuilderX/stratum/pkg/storage/objectstore"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
)

type objectPutter interface {
	PutObject(context.Context, string, string, io.Reader, int64, minio.PutObjectOptions) (minio.UploadInfo, error)
}

type bucketManager interface {
	BucketExists(context.Context, string) (bool, error)
	MakeBucket(context.Context, string, minio.MakeBucketOptions) error
}

type encryptedClientAdapter struct {
	client interface {
		PutObject(context.Context, string, string, io.Reader, int64, minio.PutObjectOptions) (minio.UploadInfo, error)
		GetObject(context.Context, string, string, minio.GetObjectOptions) (*minio.Object, error)
		RemoveObject(context.Context, string, string, minio.RemoveObjectOptions) error
	}
}

func (a encryptedClientAdapter) PutObject(ctx context.Context, bucket, key string, reader io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
	return a.client.PutObject(ctx, bucket, key, reader, size, opts)
}
func (a encryptedClientAdapter) GetObject(ctx context.Context, bucket, key string, opts minio.GetObjectOptions) (io.ReadCloser, error) {
	return a.client.GetObject(ctx, bucket, key, opts)
}
func (a encryptedClientAdapter) RemoveObject(ctx context.Context, bucket, key string, opts minio.RemoveObjectOptions) error {
	return a.client.RemoveObject(ctx, bucket, key, opts)
}

type Store struct {
	client  objectPutter
	bucket  string
	key     [32]byte
	generic *genericstore.EncryptedStore
}

func NewStore(client objectPutter, bucket string, key [32]byte) *Store {
	s := &Store{client: client, bucket: bucket, key: key}
	if full, ok := client.(interface {
		PutObject(context.Context, string, string, io.Reader, int64, minio.PutObjectOptions) (minio.UploadInfo, error)
		GetObject(context.Context, string, string, minio.GetObjectOptions) (*minio.Object, error)
		RemoveObject(context.Context, string, string, minio.RemoveObjectOptions) error
	}); ok {
		s.generic = genericstore.NewEncryptedStore(encryptedClientAdapter{client: full}, bucket, key)
	}
	return s
}

func (s *Store) EnsureBucket(ctx context.Context) error {
	manager, ok := s.client.(bucketManager)
	if !ok {
		return fmt.Errorf("trace payload bucket management unavailable")
	}
	exists, err := manager.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("check trace payload bucket: %w", err)
	}
	if exists {
		return nil
	}
	if err := manager.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("create trace payload bucket: %w", err)
	}
	return nil
}

func (s *Store) Put(ctx context.Context, payload port.TracePayload) (port.TracePayloadRef, error) {
	if s == nil || s.client == nil || s.bucket == "" {
		return port.TracePayloadRef{}, fmt.Errorf("trace payload store unavailable")
	}
	if s.generic != nil {
		raw, _ := observability.SanitizedTracePayload(payload.Value)
		ref, err := s.generic.Put(ctx, genericstore.Payload{TenantID: payload.TenantID, Namespace: payload.TraceID, ID: payload.Kind, Value: json.RawMessage(raw)})
		if err != nil {
			return port.TracePayloadRef{}, err
		}
		return port.TracePayloadRef{Reference: ref.URI, SHA256: ref.SHA256, SizeBytes: ref.SizeBytes}, nil
	}
	raw, hash := observability.SanitizedTracePayload(payload.Value)
	encrypted, err := pkgcrypto.Encrypt(s.key, string(raw))
	if err != nil {
		return port.TracePayloadRef{}, fmt.Errorf("encrypt trace payload: %w", err)
	}
	objectKey := strings.Join([]string{
		safeSegment(payload.TenantID), safeSegment(payload.TraceID),
		uuid.Must(uuid.NewV7()).String() + "-" + safeSegment(payload.Kind) + ".enc",
	}, "/")
	body := []byte(encrypted)
	_, err = s.client.PutObject(ctx, s.bucket, objectKey, bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{
		ContentType:  "application/octet-stream",
		UserMetadata: map[string]string{"sha256": hash, "original-content-type": "application/json"},
	})
	if err != nil {
		return port.TracePayloadRef{}, fmt.Errorf("put trace payload: %w", err)
	}
	return port.TracePayloadRef{
		Reference: "object://" + s.bucket + "/" + objectKey,
		SHA256:    hash, SizeBytes: int64(len(raw)),
	}, nil
}

func safeSegment(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, "..", "_")
	if value == "" {
		return "unknown"
	}
	return value
}
