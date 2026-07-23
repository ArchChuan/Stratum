package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/byteBuilderX/stratum/pkg/constants"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
)

type Payload struct {
	TenantID, Namespace, ID string
	Value                   any
}
type Reference struct {
	URI, SHA256 string
	SizeBytes   int64
}
type Store interface {
	Put(context.Context, Payload) (Reference, error)
	Get(context.Context, Reference) ([]byte, error)
	Delete(context.Context, Reference) error
}

type client interface {
	PutObject(context.Context, string, string, io.Reader, int64, minio.PutObjectOptions) (minio.UploadInfo, error)
	GetObject(context.Context, string, string, minio.GetObjectOptions) (io.ReadCloser, error)
	RemoveObject(context.Context, string, string, minio.RemoveObjectOptions) error
}

type EncryptedStore struct {
	client   client
	bucket   string
	key      [32]byte
	maxBytes int64
}

func New(client client, bucket string, key [32]byte) *EncryptedStore {
	return &EncryptedStore{client: client, bucket: bucket, key: key, maxBytes: constants.MaxEncryptedObjectBytes}
}
func NewEncryptedStore(client client, bucket string, key [32]byte) *EncryptedStore {
	return New(client, bucket, key)
}

func (s *EncryptedStore) Put(ctx context.Context, p Payload) (Reference, error) {
	if s == nil || s.client == nil || s.bucket == "" {
		return Reference{}, fmt.Errorf("object store unavailable")
	}
	raw, err := json.Marshal(p.Value)
	if err != nil {
		return Reference{}, fmt.Errorf("marshal payload: %w", err)
	}
	h := sha256.Sum256(raw)
	hash := hex.EncodeToString(h[:])
	enc, err := pkgcrypto.Encrypt(s.key, string(raw))
	if err != nil {
		return Reference{}, fmt.Errorf("encrypt payload: %w", err)
	}
	key := safe(p.TenantID) + "/" + safe(p.Namespace) + "/" + safe(p.ID) + "/" + uuid.Must(uuid.NewV7()).String() + ".enc"
	_, err = s.client.PutObject(ctx, s.bucket, key, bytes.NewReader([]byte(enc)), int64(len(enc)), minio.PutObjectOptions{ContentType: "application/octet-stream", UserMetadata: map[string]string{"sha256": hash, "original-content-type": "application/json"}})
	if err != nil {
		return Reference{}, fmt.Errorf("put payload: %w", err)
	}
	return Reference{URI: "object://" + s.bucket + "/" + key, SHA256: hash, SizeBytes: int64(len(raw))}, nil
}

func (s *EncryptedStore) Get(ctx context.Context, ref Reference) ([]byte, error) {
	bucket, key, err := s.parse(ref.URI)
	if err != nil {
		return nil, err
	}
	obj, err := s.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get payload: %w", err)
	}
	defer obj.Close()
	data, err := io.ReadAll(io.LimitReader(obj, s.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}
	if int64(len(data)) > s.maxBytes {
		return nil, fmt.Errorf("payload too large")
	}
	plain, err := pkgcrypto.Decrypt(s.key, string(data))
	if err != nil {
		return nil, fmt.Errorf("decrypt payload: %w", err)
	}
	raw := []byte(plain)
	h := sha256.Sum256(raw)
	got := hex.EncodeToString(h[:])
	if ref.SHA256 != "" && !strings.EqualFold(got, ref.SHA256) {
		return nil, fmt.Errorf("payload hash mismatch")
	}
	return raw, nil
}

func (s *EncryptedStore) Delete(ctx context.Context, ref Reference) error {
	bucket, key, err := s.parse(ref.URI)
	if err != nil {
		return err
	}
	if err := s.client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("delete payload: %w", err)
	}
	return nil
}
func (s *EncryptedStore) parse(raw string) (string, string, error) {
	if s == nil || s.client == nil {
		return "", "", fmt.Errorf("object store unavailable")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "object" || u.Host != s.bucket || u.Path == "" || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !strings.HasPrefix(u.Path, "/") || strings.HasSuffix(u.Path, "/") || strings.Contains(u.Path, "//") {
		return "", "", fmt.Errorf("invalid object reference")
	}
	key := strings.TrimPrefix(u.Path, "/")
	for _, segment := range strings.Split(key, "/") {
		if segment == "." || segment == ".." || segment == "" {
			return "", "", fmt.Errorf("invalid object reference")
		}
	}
	return u.Host, key, nil
}
func safe(v string) string {
	v = strings.TrimSpace(v)
	v = strings.ReplaceAll(v, "/", "_")
	v = strings.ReplaceAll(v, "..", "_")
	if v == "" {
		return "unknown"
	}
	return v
}
