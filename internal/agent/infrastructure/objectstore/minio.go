package objectstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/byteBuilderX/stratum/pkg/observability"
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

type Store struct {
	client objectPutter
	bucket string
	key    [32]byte
}

func NewStore(client objectPutter, bucket string, key [32]byte) *Store {
	return &Store{client: client, bucket: bucket, key: key}
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
